.PHONY: vllm-install
vllm-install:
	@echo "==> Cloning vllm repository (branch: releases/v0.12.0)..."
	rm -rf /tmp/vllm
	git clone -b releases/v0.12.0 https://github.com/vllm-project/vllm.git /tmp/vllm
	
	@echo "==> Copying patch file..."
	cp ./patches/vllm/vllm_v0.12.0.patch /tmp/vllm_v0.12.0.patch
	
	@echo "==> Building and installing vllm..."
	cd /tmp/vllm && \
	export VLLM_PRECOMPILED_WHEEL_COMMIT=$$(git rev-parse HEAD) && \
	export VLLM_USE_PRECOMPILED=1 && \
	patch -p1 < /tmp/vllm_v0.12.0.patch && \
	pip install . --no-deps --no-build-isolation -v

	rm -rf /tmp/vllm
	rm -f /tmp/vllm_v0.12.0.patch

.PHONY: llumlet-install
llumlet-install:
	cd ./python/llumnix && make vllm_install && make proto
	cp ./patches/vllm/mooncake/mooncake_connector_v1.py /usr/local/lib/python3.12/dist-packages/mooncake/mooncake_connector_v1.py

.PHONY: lib-tokenizers-build
lib-tokenizers-build:
	cd ./lib/sgl-model-gateway/sgl-model-gateway/bindings/golang && make build

.PHONY: gateway-proto-build
gateway-proto-build:
	@protoc --go_out=./pkg/cms/ \
    	--proto_path="./pkg" \
    	./pkg/cms/proto/cms.proto
	@echo "Compiling ./pkg/cms/proto/cms.proto"

	@protoc --go_out=./pkg/scheduler/llumlet/ \
       --go-grpc_out=./pkg/scheduler/llumlet/ \
       --proto_path="./pkg" \
		./pkg/scheduler/llumlet/proto/llumlet_server.proto
	@echo "Compiling ./pkg/scheduler/llumlet/proto/llumlet_server.proto"

	@protoc --go_out=./pkg/resolver/ \
       --go-grpc_out=./pkg/resolver/ \
       --proto_path="./pkg" \
    	./pkg/resolver/proto/redis_discovery.proto
	@echo "Compiling ./pkg/resolver/proto/redis_discovery.proto"

.PHONY: scheduler-proto-build
scheduler-proto-build: gateway-proto-build

.PHONY: gateway-build
gateway-build: gateway-proto-build
	@echo "Building gateway..."
	@CGO_ENABLED=1 go build -buildvcs=false -ldflags="-extldflags '-L./lib/sgl-model-gateway/sgl-model-gateway/bindings/golang/target/release/'" -o bin/gateway ./cmd/gateway
	@echo "Building gateway, done"

@PHONY: scheduler-build
scheduler-build: scheduler-proto-build
	@echo "Building scheduler..."
	@CGO_ENABLED=1 go build -buildvcs=false -ldflags="-extldflags '-L./lib/sgl-model-gateway/sgl-model-gateway/bindings/golang/target/release/'" -o bin/scheduler ./cmd/scheduler
	@echo "Building scheduler, done"

.PHONY: discovery-proto-build
discovery-proto-build:
	cd ./python/discovery && make proto

.PHONY: discovery-install
discovery-install:
	cd ./python/discovery && make install

.PHONY: blade-kvt-install
blade-kvt-install:
	./scripts/install_blade_kvt.sh

.PHONY: simple-tests
simple-tests: discovery-proto-build gateway-build scheduler-build
	pytest -x -v -s ./tests/local/vllm_e2e.py::test_simple_requests

.PHONY: migration-tests
migration-tests: gateway-build scheduler-build
	pytest -x -v -s ./tests/local/vllm_e2e.py::test_migration

.PHONY: migration-correctness-tests
migration-correctness-tests: gateway-build scheduler-build
	pytest -x -v -s ./tests/local/vllm_mig_correctness.py::test_migration_correctness

.PHONY: e2e-test
e2e-test: discovery-proto-build gateway-build scheduler-build simple-tests migration-tests

TEST_DIRS := $(shell go list ./pkg/... | grep -v "/kvs/v6d" | grep -v "/kvs/mooncake")

.PHONY: unit-test
unit-test: discovery-proto-build gateway-build scheduler-build
	CGO_ENABLED=1 \
	CGO_LDFLAGS="-L./lib/sgl-model-gateway/sgl-model-gateway/bindings/golang/target/release" \
	go test -v -failfast $(TEST_DIRS) 2>&1 | grep -v "no test files"
