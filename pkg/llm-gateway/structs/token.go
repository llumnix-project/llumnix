package structs

import (
	"easgo/pkg/llm-gateway/consts"
	"fmt"
	"strings"

	"k8s.io/klog/v2"
)

type Token struct {
	Version      int64    `json:"version"`
	Model        string   `json:"model"`
	InferMode    string   `json:"infer_mode"`
	InferBackend string   `json:"infer_backend"`
	InstName     string   `json:"instance_name"`
	WorkerId     string   `json:"worker_id"`
	Policy       string   `json:"policy"`
	Endpoint     Endpoint `json:"endpoint"`
	Count        int      `json:"count"`
	NeedRelease  bool     `json:"need_release"`
}

func (t *Token) Id() string {
	return t.Endpoint.Description()
}

func (t *Token) Ver() int64 {
	return t.Version
}

func (t *Token) String() string {
	str := t.Endpoint.Description()

	if len(t.Model) > 0 {
		str += fmt.Sprintf(",model:%s", t.Model)
	}
	if len(t.InferMode) > 0 {
		str += fmt.Sprintf(",infer_mode:%s", t.InferMode)
	}
	if len(t.InferBackend) > 0 {
		str += fmt.Sprintf(",infer_backend:%s", t.InferBackend)
	}
	if len(t.InstName) > 0 {
		str += fmt.Sprintf(",instance_name:%s", t.InstName)
	}
	if len(t.Policy) > 0 {
		str += fmt.Sprintf(",policy:%s", t.Policy)
	}
	if len(t.WorkerId) > 0 {
		str += fmt.Sprintf(",worker_id:%s", t.WorkerId)
	}
	str += fmt.Sprintf(",need_release:%v", t.NeedRelease)
	return str
}

type TokenRequest struct {
	Id             string   `json:"id"`
	Model          string   `json:"model"`
	Count          int      `json:"count"`
	LocalEndpoint  Endpoint `json:"local_endpoint,omitempty"`
	InferMode      string   `json:"infer_mode"`
	MsgLength      int64    `json:"msg_length"`
	Message        string   `json:"message"`
	PromptTokenIds []int64  `json:"prompt_token_ids"`

	// in the pd split scenario, token is the prefill node, while token2 corresponds to the decode node information
	Tokens  []Token `json:"tokens,omitempty"`
	Tokens2 []Token `json:"tokens2,omitempty"`
}

func (tr *TokenRequest) String() string {
	var (
		str string
		ts  []string
	)
	for _, token := range tr.Tokens {
		ts = append(ts, token.String())
	}
	if len(ts) > 0 {
		str = "{" + strings.Join(ts, ",") + "}"
	}
	ts = []string{}
	for _, token := range tr.Tokens2 {
		ts = append(ts, token.String())
	}
	if len(ts) > 0 {
		str += ",{" + strings.Join(ts, ",") + "}"
	}
	str += fmt.Sprintf(",%s", tr.LocalEndpoint.IP)
	return str
}

type NextTokens struct {
	Tokens     []Token
	Tokens2    []Token
	ExternalEp *ExternalEndpoint
}

func (nt *NextTokens) Description() string {
	var builder strings.Builder
	for i, t := range nt.Tokens {
		builder.WriteString(t.Endpoint.Description())
		if i < len(nt.Tokens)-1 {
			builder.WriteString(",")
		}
	}

	if len(nt.Tokens2) > 0 {
		if len(nt.Tokens) > 0 {
			builder.WriteString("|")
		}
		for i, t := range nt.Tokens2 {
			builder.WriteString(t.Endpoint.Description())
			if i < len(nt.Tokens2)-1 {
				builder.WriteString(",")
			}
		}
	}

	if nt.ExternalEp != nil {
		if len(nt.Tokens) > 0 || len(nt.Tokens2) > 0 {
			builder.WriteString("|")
		}
		builder.WriteString(nt.ExternalEp.Description())
	}

	return builder.String()
}

// check token
func (nt *NextTokens) EnsureInferMode() bool {
	if len(nt.Tokens) > 0 && len(nt.Tokens2) > 0 {
		if nt.Tokens[0].InferMode == consts.PrefillInferMode && nt.Tokens2[0].InferMode == consts.DecodeInferMode {
			return true
		} else {
			klog.Warningf("split mode data exception: %v", *nt)
			return false
		}
	}

	if len(nt.Tokens) > 0 && nt.Tokens2 == nil {
		if nt.Tokens[0].InferMode == consts.NormalInferMode || nt.Tokens[0].InferMode == "" {
			return true
		} else {
			klog.Warningf("normal mode data exception: %v", *nt)
			return false
		}
	}
	return false
}
