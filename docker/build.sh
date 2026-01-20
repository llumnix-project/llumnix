#!/bin/sh

set -e

echo "building llm-gateway for arch $GOARCH"
echo "building dir $OUTPUT"

export GOPROXY="https://goproxy.cn,direct"
export GOPRIVATE="gitlab.alibaba-inc.com"
git config --global url."git@gitlab.alibaba-inc.com:".insteadOf "https://gitlab.alibaba-inc.com/"

curl -s http://eas-data.oss-accelerate.aliyuncs.com/3rdparty/tokenizers/20260115/libsgl_model_gateway_go.linux-${GOARCH}.tar.gz -o /tmp/libsgl_model_gateway_go.tar.gz
tar -xvf /tmp/libsgl_model_gateway_go.tar.gz -C /tmp

echo "go env: `go env GOPROXY`"
CGO_ENABLED=1 CGO_LDFLAGS="-L/tmp -lm" go build -o ${OUTPUT}/llm-gateway ./cmd/llm-gateway
