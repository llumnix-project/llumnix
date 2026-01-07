package service

import (
	"easgo/pkg/llm-gateway/structs"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/klog/v2"
)

type RequestTime struct {
	endpoint        structs.Endpoint
	noResponseCnt   int
	lastRequestTime time.Time
}

type NoResponseReqCollector struct {
	noResponseRecorder map[structs.Endpoint]*RequestTime
	sum                atomic.Int64
	mu                 sync.Mutex
}

func NewNoResponseReqCollector() *NoResponseReqCollector {
	return &NoResponseReqCollector{
		noResponseRecorder: make(map[structs.Endpoint]*RequestTime),
	}
}

func (nrc *NoResponseReqCollector) record(endpoint structs.Endpoint) {
	nrc.mu.Lock()
	defer nrc.mu.Unlock()

	rt, ok := nrc.noResponseRecorder[endpoint]
	if ok {
		rt.noResponseCnt += 1
		rt.lastRequestTime = time.Now()
	} else {
		nrc.noResponseRecorder[endpoint] = &RequestTime{
			endpoint:        endpoint,
			noResponseCnt:   1,
			lastRequestTime: time.Now(),
		}
	}
	nrc.sum.Add(1)
}

func (nrc *NoResponseReqCollector) erase(endpoint structs.Endpoint) {
	nrc.mu.Lock()
	defer nrc.mu.Unlock()

	rt, ok := nrc.noResponseRecorder[endpoint]
	if !ok {
		panic(fmt.Sprintf("no request recorder, expected ep: %s, current eps: %v", endpoint.String(), nrc.getSortedEndpoints()))
	}

	if rt.noResponseCnt == 1 {
		delete(nrc.noResponseRecorder, endpoint)
	} else {
		rt.noResponseCnt -= 1
	}
	nrc.sum.Add(-1)
}

func (nrc *NoResponseReqCollector) getSortedEndpoints() []structs.Endpoint {
	var noQRecorder []RequestTime
	nrc.mu.Lock()
	for _, r := range nrc.noResponseRecorder {
		noQRecorder = append(noQRecorder, *r)
	}
	nrc.mu.Unlock()

	sort.Slice(noQRecorder, func(i, j int) bool {
		if noQRecorder[i].noResponseCnt == noQRecorder[j].noResponseCnt {
			return noQRecorder[i].lastRequestTime.Before(noQRecorder[j].lastRequestTime)
		}
		return noQRecorder[i].noResponseCnt < noQRecorder[j].noResponseCnt
	})

	var endpoints []structs.Endpoint
	for _, r := range noQRecorder {
		endpoints = append(endpoints, r.endpoint)
	}
	return endpoints
}

func (nrc *NoResponseReqCollector) getTotalCount() int64 {
	return nrc.sum.Load()
}

type TokenFilter struct {
	mu           sync.Mutex
	collectorMap map[string]*NoResponseReqCollector
}

func NewTokenFilter() *TokenFilter {
	return &TokenFilter{
		collectorMap: make(map[string]*NoResponseReqCollector),
	}
}

func (tf *TokenFilter) getOrCreateCollector(model string) *NoResponseReqCollector {
	tf.mu.Lock()
	defer tf.mu.Unlock()

	c, ok := tf.collectorMap[model]
	if ok {
		return c
	} else {
		c = NewNoResponseReqCollector()
		tf.collectorMap[model] = c
		return c
	}
}

func (tf *TokenFilter) FilterTokenImpl(collector *NoResponseReqCollector, tokens []structs.Token) structs.Token {
	noRespEndpoints := collector.getSortedEndpoints()
	if len(noRespEndpoints) == 0 {
		return tokens[0]
	}

	for _, t := range tokens {
		match := false
		for _, e := range noRespEndpoints {
			if e == t.Endpoint {
				match = true
				break
			}
		}
		if !match {
			return t
		}
	}

	for _, e := range noRespEndpoints {
		for _, t := range tokens {
			if e == t.Endpoint {
				return t
			}
		}
	}
	return tokens[0]

}

func (tf *TokenFilter) FilterModelTokens(nextToken *structs.NextTokens, model string) (*structs.Token, *structs.Token) {
	if nextToken == nil || len(nextToken.Tokens) == 0 {
		klog.Errorf("invalid filter input: %v", *nextToken)
		return nil, nil
	}
	nextToken.EnsureInferMode()

	modelCollector := tf.getOrCreateCollector(model)
	token := tf.FilterTokenImpl(modelCollector, nextToken.Tokens)
	if nextToken.Tokens2 != nil && len(nextToken.Tokens2) > 0 {
		token2 := tf.FilterTokenImpl(modelCollector, nextToken.Tokens2)
		return &token, &token2
	} else {
		return &token, nil
	}
}

func (tf *TokenFilter) Record(endpoint structs.Endpoint, model string) {
	modelCollector := tf.getOrCreateCollector(model)
	modelCollector.record(endpoint)
}

func (tf *TokenFilter) Erase(endpoint structs.Endpoint, model string) {
	modelCollector := tf.getOrCreateCollector(model)
	modelCollector.erase(endpoint)
}

func (tf *TokenFilter) GetModelTotalCount(model string) int64 {
	tf.mu.Lock()
	defer tf.mu.Unlock()

	c, ok := tf.collectorMap[model]
	if ok {
		return c.getTotalCount()
	} else {
		return 0
	}
}

func (tf *TokenFilter) GetTotalCount() int64 {
	var count int64
	tf.mu.Lock()
	defer tf.mu.Unlock()

	for _, c := range tf.collectorMap {
		count += c.getTotalCount()
	}
	return count
}
