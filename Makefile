.PHONY: vllm-install
vllm-install:
	@echo "==> Cloning vllm repository (branch: releases/v0.12.0)..."
	rm -rf /tmp/vllm
	git clone -b releases/v0.12.0 https://github.com/vllm-project/vllm.git /tmp/vllm
	
	@echo "==> Copying patch file..."
	cp ./python/llumnix/patches/vllm/vllm_v0.12.0.patch /tmp/vllm_v0.12.0.patch
	
	@echo "==> Building and installing vllm..."
	cd /tmp/vllm && \
	export VLLM_PRECOMPILED_WHEEL_COMMIT=$$(git rev-parse HEAD) && \
	export VLLM_USE_PRECOMPILED=1 && \
	patch -p1 < /tmp/vllm_v0.12.0.patch && \
	pip install . --no-deps --no-build-isolation -v

	rm -rf /tmp/vllm
	rm -f /tmp/vllm_v0.12.0.patch

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

.PHONY: simple-tests
simple-tests: runtime-proto-build llm-gateway-build
	pytest -x -v -s /mnt/eas/cuikuilong/llumnix/tests/local/vllm_e2e.py::test_simple_requests

.PHONY: migration-tests
migration-tests: llm-gateway-build
	pytest -x -v -s /mnt/eas/cuikuilong/llumnix/tests/local/vllm_e2e.py::test_migration

.PHONY: e2e-tests
e2e-tests: runtime-proto-build llm-gateway-build simple-tests migration-tests

TEST_DIRS := $(shell go list ./pkg/llm-gateway/... | grep -v "/lrs" | grep -v "/kvs")

.PHONY: llumnix-unit-test
llumnix-unit-test: llm-gateway-proto-build
	CGO_ENABLED=1 \
	CGO_LDFLAGS="-L./lib/sgl-model-gateway/sgl-model-gateway/bindings/golang/target/release" \
	go test -v -failfast $(TEST_DIRS) 2>&1 | grep -v "no test files"
