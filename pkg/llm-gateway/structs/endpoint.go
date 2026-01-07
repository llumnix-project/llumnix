package structs

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Endpoint struct {
	IP         string `json:"ip"`
	Port       int    `json:"port"`
	OptionPort int    `json:"option_port,omitempty"`
}

func NewEndpoint(str string) (ep Endpoint, err error) {
	arr := strings.Split(str, ":")
	if len(arr) != 2 {
		err = fmt.Errorf("arr length %v not match", len(arr))
		return
	}
	ep.IP = arr[0]
	ep.Port, _ = strconv.Atoi(arr[1])
	return
}

func (ep *Endpoint) String() string {
	return fmt.Sprintf("%s:%d", ep.IP, ep.Port)
}

func (ep *Endpoint) Description() string {
	return fmt.Sprintf("%s:%d-%d", ep.IP, ep.Port, ep.OptionPort)
}

type WeightEndpoint struct {
	Ep     Endpoint   `json:"endpoint"`
	Weight int        `json:"weight"`
	Worker *LLMWorker `json:"worker,omitempty"`
}

func NewWeightEndpoint(str string) (wep WeightEndpoint, err error) {
	arr := strings.Split(str, "_")
	if wep.Ep, err = NewEndpoint((arr[0])); err != nil {
		return
	}
	if len(arr) == 2 {
		wep.Weight, err = strconv.Atoi(arr[1])
	} else {
		wep.Weight = 100
	}
	return
}

func (wep *WeightEndpoint) String() string {
	return fmt.Sprintf("%s_%d", wep.Ep.Description(), wep.Weight)
}

type GatewayEndpoint struct {
	Endpoint
}

func (ge *GatewayEndpoint) Id() string {
	return ge.Endpoint.String()
}

var (
	baseVersionRegex = regexp.MustCompile(`/v\d+$`)
	pathVersionRegex = regexp.MustCompile(`^/v\d+`)
)

type ExternalEndpoint struct {
	URL    string `json:"base_url"`
	APIKey string `json:"api_key"`
	Model  string `json:"model"`
}

func (eep *ExternalEndpoint) String() string { return fmt.Sprintf("%s", eep.URL) }

func (eep *ExternalEndpoint) Description() string { return fmt.Sprintf("%s", eep.URL) }

func (eep *ExternalEndpoint) JoinURL(path string) string {
	eep.URL = strings.TrimSuffix(eep.URL, "/")

	hasBaseVersion := baseVersionRegex.MatchString(eep.URL)
	if hasBaseVersion && strings.HasPrefix(path, "/v") {
		path = pathVersionRegex.ReplaceAllString(path, "")
	}

	return fmt.Sprintf("%s%s", eep.URL, path)
}
