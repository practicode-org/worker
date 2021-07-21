# syntax=docker/dockerfile:1
FROM ubuntu:latest as build-runjail

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y make golang-1.13 git

#ENV GOROOT /usr/lib/go-1.13
#ENV GOPATH /go
ENV PATH /usr/lib/go-1.13/bin:$PATH

WORKDIR /build/
COPY . .

RUN make

##################
FROM ubuntu:latest as build-nsjail

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y \
  make \
  git \
  g++ \
  flex \
  bison \
  autoconf \
  libprotobuf-dev \
  libnl-route-3-dev \
  libtool \
  make \
  pkg-config \
  protobuf-compiler

RUN git clone https://github.com/google/nsjail
COPY ./docker/nsjail-1.patch /nsjail/
WORKDIR /nsjail
RUN git apply nsjail-1.patch
RUN make

##################
FROM ubuntu:latest

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y wget lsb-release software-properties-common libprotobuf17 libnl-route-3-200
RUN bash -c "$(wget -O - https://apt.llvm.org/llvm.sh)"

COPY --from=build-runjail /build/bin/main /runjail
COPY --from=build-nsjail /nsjail/nsjail /usr/bin/nsjail
COPY ./examples/cpp-rules.json /run/cpp-rules.json

EXPOSE 1556
RUN mkdir /tmp/sources/ && chmod ugo+rwx /tmp/sources/
RUN mkdir /tmp/out
ENTRYPOINT /runjail -rules /run/cpp-rules.json