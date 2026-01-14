package types

import (
	"fmt"
	"strconv"
	"strings"
)

// Endpoint represents a network endpoint with Host address and port.
// support both ip and domain, for example:
// 192.168.0.1:8080 and localhost:8080
type Endpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// EndpointSlice is a slice of Endpoint
type EndpointSlice []Endpoint

// NewEndpoint creates an Endpoint from a string in format "host:port".
func NewEndpoint(str string) (ep Endpoint, err error) {
	parts := strings.Split(str, ":")
	if len(parts) != 2 {
		err = fmt.Errorf("invalid endpoint format: %s, expected 'host:port'", str)
		return
	}
	ep.Host = parts[0]
	// Parse Port
	ep.Port, err = strconv.Atoi(parts[1])
	if err != nil {
		err = fmt.Errorf("invalid port: %s", parts[1])
		return
	}
	return
}

// String returns the string in format "host:port".
func (ep Endpoint) String() string {
	return fmt.Sprintf("%s:%d", ep.Host, ep.Port)
}
