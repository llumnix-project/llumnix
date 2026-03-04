module llm-gateway

go 1.23.0

toolchain go1.23.12

require (
	github.com/agiledragon/gomonkey/v2 v2.14.0
	github.com/aliyun/aliyun-oss-go-sdk v3.0.2+incompatible
	github.com/aliyun/credentials-go v1.4.11
	github.com/cespare/xxhash/v2 v2.3.0
	github.com/fatih/color v1.18.0
	github.com/go-playground/validator/v10 v10.0.0-00010101000000-000000000000
	github.com/go-redis/redismock/v9 v9.2.0
	github.com/google/uuid v1.6.0
	github.com/gorilla/mux v1.8.1
	github.com/gorilla/websocket v1.5.3
	github.com/imdario/mergo v0.3.5
	github.com/influxdata/influxdb v1.12.2
	github.com/json-iterator/go v1.1.12
	github.com/modern-go/reflect2 v1.0.2
	github.com/nwidger/jsoncolor v0.3.2
	github.com/pkg/errors v0.9.1
	github.com/prometheus/common v0.53.0
	github.com/redis/go-redis/v9 v9.18.0
	github.com/sglang/sglang-go-grpc-sdk v0.0.0-00010101000000-000000000000
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.10
	github.com/stretchr/testify v1.11.1
	github.com/tmaxmax/go-sse v0.11.0
	github.com/valyala/fastjson v0.0.0-00010101000000-000000000000
	github.com/wI2L/jsondiff v0.7.0
	golang.org/x/exp v0.0.0-20240604190554-fc45aab8b7f8
	golang.org/x/sync v0.12.0
	gonum.org/v1/gonum v0.12.0
	google.golang.org/grpc v1.69.2
	google.golang.org/protobuf v1.35.1
	gotest.tools v2.2.0+incompatible
	k8s.io/apimachinery v0.32.2
	k8s.io/klog/v2 v2.130.1
)

require (
	github.com/alibabacloud-go/debug v1.0.1 // indirect
	github.com/alibabacloud-go/tea v1.2.2 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.4 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/rogpeppe/go-internal v1.12.0 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/net v0.38.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	golang.org/x/time v0.7.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20241015192408-796eee8c2d53 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/sglang/sglang-go-grpc-sdk => gitlab.alibaba-inc.com/eas/sgl-model-gateway/sgl-model-gateway/bindings/golang v0.0.0-20260112090807-a24c9f1c9c16

// Force downgrade dependencies to versions compatible with go 1.23
replace (
	github.com/go-playground/validator/v10 => github.com/go-playground/validator/v10 v10.20.0
	github.com/imdario/mergo => github.com/imdario/mergo v0.3.16
	github.com/valyala/fastjson => github.com/valyala/fastjson v1.6.4
	github.com/xuri/excelize/v2 => github.com/xuri/excelize/v2 v2.9.0
	golang.org/x/exp => golang.org/x/exp v0.0.0-20240719175910-8a7402abbf56
	gonum.org/v1/gonum => gonum.org/v1/gonum v0.15.0
	k8s.io/api => k8s.io/api v0.32.2
	k8s.io/apimachinery => k8s.io/apimachinery v0.32.2
	k8s.io/client-go => k8s.io/client-go v0.32.2
)
