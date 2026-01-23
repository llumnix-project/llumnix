.PHONY: lib-tokenizers-build
lib-tokenizers-build:
	cd ./lib/sgl-model-gateway/sgl-model-gateway/bindings/golang && make build

.PHONY: llm-gateway-proto-build
llm-gateway-proto-build:
	@protoc --go_out=./pkg/llm-gateway/cms/ \
    	--proto_path="./pkg" \
    	./pkg/llm-gateway/cms/proto/cms.proto
	@echo "Compiling ./pkg/llm-gateway/cms/proto/cms.proto"

	@protoc --go_out=./pkg/llm-gateway/llumlet/ \
       --go-grpc_out=./pkg/llm-gateway/llumlet/ \
       --proto_path="./pkg" \
		./pkg/llm-gateway/llumlet/proto/llumlet_server.proto
	@echo "Compiling ./pkg/llm-gateway/llumlet/proto/llumlet_server.proto"

	@protoc --go_out=./pkg/llm-gateway/resolver/ \
       --go-grpc_out=./pkg/llm-gateway/resolver/ \
       --proto_path="./pkg" \
    	./pkg/llm-gateway/resolver/proto/redis_discovery.proto
	@echo "Compiling ./pkg/llm-gateway/resolver/proto/redis_discovery.proto"

.PHONY: llm-gateway-build
llm-gateway-build: llm-gateway-proto-build
	@echo "Building llm-gateway..."
	@CGO_ENABLED=1 go build -buildvcs=false -ldflags="-extldflags '-L./lib/sgl-model-gateway/sgl-model-gateway/bindings/golang/target/release/'" -o bin/llm-gateway ./cmd/llm-gateway
	@echo "Building llm-gateway, done"

.PHONY: llumlet-install
llumlet-install:
	cd ./python/llumnix && make vllm_install && make proto

.PHONY: runtime-proto-build
runtime-proto-build:
	cd ./python/runtime && make proto

.PHONY: simple-test
simple-test:
	pytest -x -v -s /mnt/eas/cuikuilong/llumnix/tests/local/vllm_e2e.py::test_simple_requests

.PHONY: migration-test
migration-test:
	pytest -x -v -s /mnt/eas/cuikuilong/llumnix/tests/local/vllm_e2e.py::test_migration

.PHONY: test
test: runtime-proto-build llm-gateway-build simple-test migration-test

.PHONY: llumnix-unit-test
llumnix-unit-test: llm-gateway-proto-build
	CGO_ENABLED=1 \
	CGO_LDFLAGS="-L$(CURDIR)/lib/sgl-model-gateway/sgl-model-gateway/bindings/golang/target/release" \
	go test -v -failfast -cover \
		./pkg/llm-gateway/cms \
		./pkg/llm-gateway/llumlet \
		./pkg/llm-gateway/schedule-policy/llumnix \
		./pkg/llm-gateway/kvs \
		./pkg/llm-gateway/load-balancer