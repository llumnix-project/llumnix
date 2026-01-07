.PHONY: llm-gateway-proto-build
llm-gateway-proto-build:
	protoc --go_out=./pkg/llm-gateway/cms/ \
        --proto_path="./pkg" \
        ./pkg/llm-gateway/cms/proto/cms.proto

	protoc --go_out=./pkg/llm-gateway/llumlet/ \
       --go-grpc_out=./pkg/llm-gateway/llumlet/ \
       --proto_path="./pkg" \
    	./pkg/llm-gateway/llumlet/proto/llumlet_server.proto

.PHONY: llm-gateway-build
llm-gateway-build: llm-gateway-proto-build
	CGO_ENABLED=1 go build -buildvcs=false -ldflags="-extldflags '-L./lib/tokenizers'" -o bin/llm-gateway ./cmd/llm-gateway

.PHONY: llumnix-unit-test
llumnix-unit-test: llm-gateway-proto-build
	go test -v -failfast -cover ./pkg/llm-gateway/cms ./pkg/llm-gateway/llumlet ./pkg/llm-gateway/schedule-policy/llumnix ./pkg/llm-gateway/kvs ./pkg/llm-gateway/load-balancer
