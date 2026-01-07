# syntax=docker/dockerfile:1.4

FROM registry.cn-hangzhou.aliyuncs.com/eas/golang:1.21.5 as builder-go
ARG TARGETPLATFORM
ARG VERSION=v0.6.0
WORKDIR /workspace
RUN curl -fsSL https://github.com/daulet/tokenizers/releases/download/${VERSION}/libtokenizers_rs.$(echo ${TARGETPLATFORM} | tr / -).tar.gz | tar xvz
COPY ./example .
COPY ./test/data ./test/data
RUN go mod download
RUN mv ./libtokenizers_rs.a /go/pkg/mod/github.com/daulet/tokenizers@${VERSION}/lib/$(echo ${TARGETPLATFORM} | tr / -)/libtokenizers_rs.a
# mounting Go cache won't work since we mutate it above
RUN go run main.go
