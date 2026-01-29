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

.PHONY: gateway-build
gateway-build: llm-gateway-proto-build
	@echo "Building llm-gateway..."
	@CGO_ENABLED=1 go build -buildvcs=false -ldflags="-extldflags '-L./lib/sgl-model-gateway/sgl-model-gateway/bindings/golang/target/release/'" -o bin/llm-gateway ./cmd/llm-gateway
	@echo "Building llm-gateway, done"

.PHONY: discovery-proto-build
discovery-proto-build:
	cd ./python/discovery && make proto

.PHONY: discovery-install
discovery-install:
	cd ./python/discovery && make install

.PHONY: blade-kvt-install
blade-kvt-install:
	cd ./python/blade-kvt && rm -rf build dist *.egg-info && bash ./tools/install_barex.sh && python3 setup.py bdist_wheel && pip install dist/*.whl

.PHONY: runtime-proto-build
runtime-proto-build:
	cd ./python/runtime && make proto

.PHONY: simple-tests
simple-tests: runtime-proto-build gateway-build
	pytest -x -v -s ./tests/local/vllm_e2e.py::test_simple_requests

.PHONY: migration-tests
migration-tests: gateway-build
	pytest -x -v -s ./tests/local/vllm_e2e.py::test_migration

.PHONY: e2e-tests
e2e-tests: discovery-proto-build gateway-build simple-tests migration-tests

TEST_DIRS := $(shell go list ./pkg/llm-gateway/... | grep -v "/kvs/v6d")

.PHONY: unit-test
unit-test: llm-gateway-proto-build
	CGO_ENABLED=1 \
	CGO_LDFLAGS="-L./lib/sgl-model-gateway/sgl-model-gateway/bindings/golang/target/release" \
	go test -v -failfast $(TEST_DIRS) 2>&1 | grep -v "no test files"
