package main

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/practicode-org/worker/src/api"
	"github.com/practicode-org/worker/src/rules"
	log "github.com/sirupsen/logrus"

	"github.com/gorilla/websocket"
)

func messageRecvLoop(conn *websocket.Conn, messages chan<- []byte, exitSignal int32, exitch chan<- struct{}) {
	defer func() {
		exitch <- struct{}{}
	}()

	for {
		// TODO: use conn.readLimit
		_, data, err := conn.ReadMessage()
		if err != nil {
			if _, ok := err.(*websocket.CloseError); ok {
				log.Warningf("Connection with the backend closed: %s", err)
				break
			}
			if atomic.LoadInt32(&exitSignal) == 1 {
				break
			}
			log.Errorf("messageRecvLoop: ReadMessage error: %v", err)
			break
		}

		log.Debug("<- received: ", trimLongString(string(data), 194))

		messages <- data
	}
	log.Debugf("Exit from messageRecvLoop")
}

func messageSendLoop(conn *websocket.Conn, messages <-chan interface{}, exitch chan<- struct{}) {
	defer func() {
		exitch <- struct{}{}
	}()

	for {
		msg := <-messages
		if _, close_ := msg.(CloseEvent); close_ {
			break
		}

		// error messages also go to stdout
		if errMsg, ok := msg.(api.Error); ok {
			log.Error("Sending error:", errMsg.Desc)
		}

		bytes, err := json.Marshal(&msg)
		if err != nil {
			log.Errorf("Failed to marshal outgoing message: %v\n", err)
			continue
		}

		log.Debug("-> sending: ", trimLongString(string(bytes), 64))

		err = conn.WriteMessage(websocket.TextMessage, bytes)
		if err != nil {
			log.Errorf("Failed to write to websocket: %v\n", err)
			return
		}
	}
	log.Debugf("Exit from messageSendLoop")
}

func handleBackendConnection(conn *websocket.Conn) {
	recvMessages := make(chan []byte, 4)
	recvExited := make(chan struct{})
	recvExitSignal := int32(0)
	go messageRecvLoop(conn, recvMessages, recvExitSignal, recvExited)

	sendMessages := make(chan interface{}, 256)
	sendExited := make(chan struct{})
	go messageSendLoop(conn, sendMessages, sendExited)

	for {
		var bytes []byte
		exit := false
		select {
		case bytes = <-recvMessages:
		case _ = <-recvExited:
			exit = true
		}
		if exit {
			break
		}

		// get the first message - it must be {"command":"new","request_id":"..."}
		msg := api.ClientMessage{}
		err := json.Unmarshal(bytes, &msg)
		if err != nil {
			log.Errorf("Failed to unmarshal: %v, message: %s...", err, trimLongString(string(bytes), 64))
			continue
		}

		log.Debugf("Got new request: %s\n", string(bytes))

		if msg.Command != "new" {
			log.Errorf("Got wrong first request message: %s...", trimLongString(string(bytes), 64))
			continue
		}
		// TODO: add accept message

		if msg.RequestID == "" {
			str := fmt.Sprintf("Got empty 'request_id' in the first message: %s...", trimLongString(string(bytes), 64))
			log.Error(str)
			sendMessages <- api.Error{Desc: str, Stage: "init", RequestID: msg.RequestID}
			sendMessages <- api.Finish{Finish: true, RequestID: msg.RequestID}
			continue
		}
		if msg.Target == "" {
			str := fmt.Sprintf("Got empty 'target' in the first message: %s...", trimLongString(string(bytes), 64))
			log.Error(str)
			sendMessages <- api.Error{Desc: str, Stage: "init", RequestID: msg.RequestID}
			sendMessages <- api.Finish{Finish: true, RequestID: msg.RequestID}
			continue
		}
		if msg.SourceFiles != nil {
			str := "Got unexpected source_files content in the first message from the backend"
			log.Error(str)
			sendMessages <- api.Error{Desc: str, Stage: "init", RequestID: msg.RequestID}
			sendMessages <- api.Finish{Finish: true, RequestID: msg.RequestID}
			continue
		}
		stages, err := rules.StagesForTarget(msg.Target)
		if err != nil {
			str := fmt.Sprintf("Failed to figure out rules for target %s: %v", msg.Target, err)
			log.Error(str)
			sendMessages <- api.Error{Desc: str, Stage: "init", RequestID: msg.RequestID}
			sendMessages <- api.Finish{Finish: true, RequestID: msg.RequestID}
			continue
		}

		handleRequest(msg.RequestID, msg.Target, stages, recvMessages, sendMessages)
	}

	log.Debugf("Start cleanup at handleBackendConnection")

	sendMessages <- CloseEvent{}
	<-sendExited
	time.Sleep(time.Millisecond * 1) // TODO: hack, otherwise connection is closed faster than messages are sent
	close(sendMessages)

	conn.Close()
	close(recvMessages)

	log.Debugf("Exit from handleBackendConnection")
}
