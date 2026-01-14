package balancer

import (
	"easgo/pkg/llm-gateway/resolver"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSWRR(t *testing.T) {
	ipResolver := resolver.NewIPListResolver("127.0.0.1:8080_5, 127.0.0.1:8081_3, 127.0.0.1:8082_2, 127.0.0.1:8083_1")
	lb := NewSWRRLoadBalancer(ipResolver)

	count := 11000
	mapCount := make(map[string]int)
	for i := 0; i < count; i++ {
		nt, _ := lb.GetNextTokens(nil)
		connStr := nt.Tokens[0].Endpoint.String()
		mapCount[connStr]++
	}

	for k, v := range mapCount {
		fmt.Printf("%s: %d\n", k, v)
	}

	count5 := int(5.0 / 11 * float64(count))
	count3 := int(3.0 / 11 * float64(count))
	count2 := int(2.0 / 11 * float64(count))
	count1 := int(1.0 / 11 * float64(count))

	fmt.Printf("%d, %d, %d, %d\n", count5, count3, count2, count1)

	assert.Equal(t, true,
		(mapCount["127.0.0.1:8080"] == count5) &&
			(mapCount["127.0.0.1:8081"] == count3) &&
			(mapCount["127.0.0.1:8082"] == count2) &&
			(mapCount["127.0.0.1:8083"] == count1))
}
