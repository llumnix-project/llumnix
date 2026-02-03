module llm-gateway

go 1.23.0

toolchain go1.23.7

require (
	github.com/agiledragon/gomonkey/v2 v2.13.0
	github.com/aliyun/aliyun-oss-go-sdk v3.0.2+incompatible
	github.com/aliyun/credentials-go v1.4.5
	github.com/cespare/xxhash/v2 v2.3.0
	github.com/fatih/color v1.15.0
	github.com/go-playground/validator/v10 v10.16.0
	github.com/go-redis/redismock/v9 v9.2.0
	github.com/google/uuid v1.6.0
	github.com/gorilla/mux v1.8.0
	github.com/gorilla/websocket v1.4.2
	github.com/imdario/mergo v0.3.12
	github.com/influxdata/influxdb v1.12.2
	github.com/json-iterator/go v1.1.12
	github.com/modern-go/reflect2 v1.0.2
	github.com/nwidger/jsoncolor v0.3.1
	github.com/pkg/errors v0.9.1
	github.com/redis/go-redis/v9 v9.2.0
	github.com/sglang/sglang-go-grpc-sdk v0.0.0-00010101000000-000000000000
	github.com/spf13/cobra v1.7.0
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.10.0
	github.com/tmaxmax/go-sse v0.11.0
	github.com/valyala/fastjson v1.6.3
	github.com/wI2L/jsondiff v0.1.0
	golang.org/x/exp v0.0.0-20240604190554-fc45aab8b7f8
	golang.org/x/sync v0.12.0
	gonum.org/v1/gonum v0.13.0
	google.golang.org/grpc v1.69.2
	google.golang.org/protobuf v1.36.5
	gotest.tools v2.2.0+incompatible
	k8s.io/apimachinery v0.31.0
	k8s.io/klog/v2 v2.100.1
)

require (
	github.com/alibabacloud-go/debug v1.0.1 // indirect
	github.com/alibabacloud-go/tea v1.3.13 // indirect
	github.com/armon/go-radix v1.0.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/fsnotify/fsnotify v1.7.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.4 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/leodido/go-urn v1.2.4 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/onsi/gomega v1.33.1 // indirect
	github.com/panjf2000/ants/v2 v2.7.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/rogpeppe/go-internal v1.13.1 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	go.opentelemetry.io/otel v1.35.0 // indirect
	go.opentelemetry.io/otel/sdk v1.35.0 // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/net v0.38.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	golang.org/x/time v0.5.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20241015192408-796eee8c2d53 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/api v0.20.4 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.1 // indirect
)

replace (
	github.com/argoproj/argo => gitlab.alibaba-inc.com/eas/argo-easse v0.0.0-20250827095443-a7c92be4285a
	github.com/creack/pty => github.com/creack/pty v1.1.17
	github.com/go-logr/logr => github.com/go-logr/logr v1.2.3
	github.com/koordinator-sh/apis => gitlab.alibaba-inc.com/eas/koordinator-sh v0.0.0-20250702091248-3ce99ae80801
	github.com/redis/go-redis/v9 v9.2.0 => gitlab.alibaba-inc.com/eas/go-redis/v9 v9.0.0-rc.2.0.20230315025632-0fb9fb5d63fc
	github.com/rivo/uniseg => github.com/rivo/uniseg v0.2.0
	github.com/sglang/sglang-go-grpc-sdk => gitlab.alibaba-inc.com/eas/sgl-model-gateway/sgl-model-gateway/bindings/golang v0.0.0-20260112090807-a24c9f1c9c16
	github.com/ugorji/go => github.com/ugorji/go/codec v1.1.7
	gitlab.alibaba-inc.com/serverlessinfra/kubesharer-apis => gitlab.alibaba-inc.com/eas/kubesharer-apis v0.1.1-for-eas
	golang.org/x/crypto => golang.org/x/crypto v0.19.0
	k8s.io/api => k8s.io/api v0.20.4
	k8s.io/apimachinery => k8s.io/apimachinery v0.20.4
	k8s.io/apiserver => k8s.io/apiserver v0.20.4
	k8s.io/client-go => k8s.io/client-go v0.20.4
	k8s.io/component-base => k8s.io/component-base v0.20.4
	k8s.io/klog/v2 => k8s.io/klog/v2 v2.100.1
	k8s.io/metrics => k8s.io/metrics v0.20.4
)
