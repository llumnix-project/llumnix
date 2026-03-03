REGISTRY?=registry.cn-shanghai.aliyuncs.com/eas

ifeq ($(SHELL),cmd.exe)
    OS := Windows
else
    OS := $(shell uname -s)
endif

ifeq ($(OS),Darwin)
    LOCAL_OS := darwin
else ifeq ($(OS),Linux)
    LOCAL_OS := linux
else 
	$(error Unsupported OS: $(OS))
endif

ifdef BUILD_ARCH
	ARCH = $(BUILD_ARCH)
else
	ARCH = $(shell go env GOARCH)
endif

ifeq ($(ARCH),amd64)
    LOCAL_ARCH := amd64
else ifeq ($(ARCH),arm64)
    LOCAL_ARCH := arm64
else
    $(error Unsupported architecture: $(ARCH))
endif

ifdef BUILD_GOOS
	GOOS = $(BUILD_GOOS)
else
	GOOS = linux
endif

GOARCH = ${ARCH}
GOCMD=go
GOTEST=$(GOCMD) test -v -failfast -cover
GOBUILD=$(GOCMD) build $(VENDOR)
PKG=llm-gateway
DOCKER ?= docker


BASE_TARGET_ARCH ?= arm64 amd64

GIT_MODS := $(shell if ! git diff-index --quiet HEAD; then echo "-dirty"; fi)
GIT_TAG := $(shell git rev-parse --short HEAD)$(GIT_MODS)

TEMP_DIR_LLM_GATEWAY := $(shell mktemp -d)
TAG_LLM_GATEWAY?=0.0.1-$(GIT_TAG)


# -------------------- LLM GATEWAY ---------------------
.PHONY: llm-gateway-docker-build
llm-gateway-docker-build:$(foreach arch,$(BASE_TARGET_ARCH),llm-gateway-docker-build-$(arch))

llm-gateway-docker-build-%: ARCH=$*
llm-gateway-docker-build-%:
	@echo "llm-gateway build $(ARCH) docker image: ${TAG_LLM_GATEWAY}"
	rm -rf $(TEMP_DIR_LLM_GATEWAY)/*
	cp -RP ./docker/* $(TEMP_DIR_LLM_GATEWAY)
	cp -RP ~/.ssh $(TEMP_DIR_LLM_GATEWAY)/.ssh
	cp -RP ~/.gitconfig $(TEMP_DIR_LLM_GATEWAY)/.gitconfig
	rsync -a ./ $(TEMP_DIR_LLM_GATEWAY)/llm-gateway --exclude .git --exclude .idea --exclude .vscode --exclude bin
	$(DOCKER) build --platform $(GOOS)/$(ARCH) --build-arg ARCH=$(ARCH) --build-arg TAG_LLM_GATEWAY=$(TAG_LLM_GATEWAY) --no-cache -t $(REGISTRY)/eas-llm-gateway-$(ARCH):$(TAG_LLM_GATEWAY) $(TEMP_DIR_LLM_GATEWAY)

.PHONY: llm-gateway-docker-push
llm-gateway-docker-push:$(foreach arch,$(BASE_TARGET_ARCH),llm-gateway-docker-push-$(arch))

llm-gateway-docker-push-%: ARCH=$*
llm-gateway-docker-push-%:
	$(DOCKER) push $(REGISTRY)/eas-llm-gateway-$(ARCH):$(TAG_LLM_GATEWAY)

llm-gateway-docker-release: llm-gateway-docker-push
	$(DOCKER) manifest create --amend $(REGISTRY)/eas-llm-gateway:$(TAG_LLM_GATEWAY) $(foreach arch,$(BASE_TARGET_ARCH),$(REGISTRY)/eas-llm-gateway-$(arch):$(TAG_LLM_GATEWAY))
	for i in $(BASE_TARGET_ARCH); do \
  		$(DOCKER) manifest annotate $(REGISTRY)/eas-llm-gateway:$(TAG_LLM_GATEWAY) $(REGISTRY)/eas-llm-gateway-$$i:$(TAG_LLM_GATEWAY) --os linux --arch $$i; \
  	done
	$(DOCKER) manifest push $(REGISTRY)/eas-llm-gateway:$(TAG_LLM_GATEWAY)

.PHONY: llm-gateway-proto-build
llm-gateway-proto-build:
	protoc --go_out=./pkg/resolver/ \
		   --go_opt=paths=source_relative \
		   --go-grpc_out=./pkg/resolver/ \
		   --go-grpc_opt=paths=source_relative \
		   --proto_path="./pkg/resolver/proto" \
		   ./pkg/resolver/proto/redis_discovery.proto

	protoc --go_out=./pkg/cms/ \
        --go_opt=paths=source_relative \
        --proto_path="./pkg/cms/proto" \
        ./pkg/cms/proto/cms.proto

	protoc --go_out=./pkg/llumlet/ \
       --go_opt=paths=source_relative \
       --go-grpc_out=./pkg/llumlet/ \
       --go-grpc_opt=paths=source_relative \
       --proto_path="./pkg/llumlet/proto" \
       ./pkg/llumlet/proto/llumlet_server.proto

.PHONY: llm-gateway-build
llm-gateway-build: lint-backend tokenizer-lib-download
	CGO_ENABLED=1 $(GOBUILD) -ldflags="-extldflags '-L/tmp -lm'" -o bin/llm-gateway $(PKG)/cmd/llm-gateway

# ------------------- Lint -------------------
.PHONY: lint-backend
lint-backend:
	@./scripts/lint-backend.sh

.PHONY: llumnix-unit-test
llumnix-unit-test: llm-gateway-proto-build
	$(GOTEST) ./pkg/cms ./pkg/llumlet ./pkg/schedule-policy/llumnix ./pkg/kvs ./pkg/load-balancer


# ------------------- Tokenizer -------------------
tokenizer-lib-download:
	@set +x; \
	if [ "$(LOCAL_OS)" = "darwin" ] && [ "$(ARCH)" = "arm64" ]; then \
		if [ ! -f /tmp/libsgl_model_gateway_go.tar.gz ] || [ "$$(md5 -q /tmp/libsgl_model_gateway_go.tar.gz)" != "4e62233aeeb84175c331a48475999a26" ]; then \
			wget https://eas-data.oss-cn-shanghai.aliyuncs.com/3rdparty/tokenizers/20260115/libsgl_model_gateway_go.darwin-aarch64.tar.gz -O /tmp/libsgl_model_gateway_go.tar.gz; \
		fi; \
	elif [ "$(LOCAL_OS)" = "linux" ]; then \
		if [ "$(ARCH)" = "amd64"  ]; then \
			if [ ! -f /tmp/libsgl_model_gateway_go.tar.gz ] || [ "$$(md5sum /tmp/libsgl_model_gateway_go.tar.gz | cut -d' ' -f1)" != "dfd3e15582d42246f9ede99f78f46799" ]; then \
				wget https://eas-data.oss-cn-shanghai.aliyuncs.com/3rdparty/tokenizers/20260115/libsgl_model_gateway_go.linux-amd64.tar.gz -O /tmp/libsgl_model_gateway_go.tar.gz; \
			fi; \
		elif [ "$(ARCH)" = "arm64" ]; then \
			if [ ! -f /tmp/libsgl_model_gateway_go.tar.gz ] || [ "$$(md5sum /tmp/libsgl_model_gateway_go.tar.gz | cut -d' ' -f1)" != "416f896d876c62bce57a54f073db89e7" ]; then \
				wget https://eas-data.oss-cn-shanghai.aliyuncs.com/3rdparty/tokenizers/20260115/libsgl_model_gateway_go.linux-arm64.tar.gz -O /tmp/libsgl_model_gateway_go.tar.gz; \
			fi; \
		fi \
	fi; \
	tar xzf /tmp/libsgl_model_gateway_go.tar.gz -C /tmp;

