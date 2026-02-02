module llumnix

require (
	github.com/agiledragon/gomonkey/v2 v2.13.0
	github.com/alibabacloud-go/tea v1.3.13 // indirect
	github.com/aliyun/aliyun-oss-go-sdk v3.0.2+incompatible
	github.com/aliyun/credentials-go v1.4.5
	github.com/fsnotify/fsnotify v1.7.0 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/uuid v1.6.0
	github.com/gorilla/mux v1.8.0
	github.com/gorilla/websocket v1.4.2 //ct
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/onsi/gomega v1.33.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/redis/go-redis/v9 v9.2.0
	github.com/sglang/sglang-go-grpc-sdk v0.0.0-00010101000000-000000000000
	github.com/spf13/cobra v1.7.0
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.10.0
	golang.org/x/net v0.34.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/text v0.21.0 // indirect
	golang.org/x/time v0.5.0 // indirect
	google.golang.org/protobuf v1.36.5
	gotest.tools v2.2.0+incompatible
	k8s.io/apimachinery v0.31.0
	k8s.io/klog/v2 v2.130.1
)

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	golang.org/x/exp v0.0.0-20230713183714-613f0c0eb8a1
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

require (
	github.com/cespare/xxhash/v2 v2.3.0
	github.com/fxamacker/cbor/v2 v2.7.0
	github.com/go-redis/redismock/v9 v9.2.0
	github.com/tmaxmax/go-sse v0.11.0
	golang.org/x/sync v0.10.0
	gonum.org/v1/gonum v0.13.0
	google.golang.org/grpc v1.69.2
)

require (
	github.com/alibabacloud-go/debug v1.0.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/rogpeppe/go-internal v1.13.1 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.opentelemetry.io/otel v1.35.0 // indirect
	go.opentelemetry.io/otel/sdk v1.35.0 // indirect
	google.golang.org/genproto v0.0.0-20230410155749-daa745c078e1 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
)

replace github.com/sglang/sglang-go-grpc-sdk => ./lib/sgl-model-gateway/sgl-model-gateway/bindings/golang

go 1.23.7
