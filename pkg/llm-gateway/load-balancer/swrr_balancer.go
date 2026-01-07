package loadbalancer

import (
	"easgo/pkg/llm-gateway/consts"
	"easgo/pkg/llm-gateway/resolver"
	"easgo/pkg/llm-gateway/structs"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// smoothWeighted is a wrapper of WeightEndpoint.
type smoothWeighted struct {
	wep             structs.WeightEndpoint
	currentWeight   int
	effectiveWeight int
}

// SWRRLoadBalancer is smooth weighted round-robin balancing algorithm
// Ref: https://github.com/phusion/nginx/commit/27e94984486058d73157038f7950a0a36ecc6e35.
type SWRRLoadBalancer struct {
	Resolver resolver.Resolver

	mu    sync.Mutex
	items []*smoothWeighted

	origEndpoints []structs.WeightEndpoint
}

// NewSWRRLoadBalancer create new SWRRLoadBalancer object
func NewSWRRLoadBalancer(r resolver.Resolver) LoadBalancer {
	lb := &SWRRLoadBalancer{
		Resolver: r,
	}

	lb.Sync()
	go func() {
		for {
			time.Sleep(time.Duration(5) * time.Second)
			lb.Sync()
		}
	}()

	return lb
}

func SameEndpoints(a, b []structs.WeightEndpoint) bool {
	if len(a) != len(b) {
		return false
	}
	aIds, bIds := getWorkerIds(a), getWorkerIds(b)
	for i := 0; i < len(aIds); i++ {
		if aIds[i] != bIds[i] {
			return false
		}
	}
	return true
}

func EndpointsToMap(endpoints []structs.WeightEndpoint) map[string]structs.WeightEndpoint {
	m := make(map[string]structs.WeightEndpoint)
	for _, ep := range endpoints {
		if ep.Worker != nil {
			m[ep.Worker.WorkerId] = ep
		} else {
			m[ep.Ep.Description()] = ep
		}
	}
	return m
}

func getWorkerIds(arr []structs.WeightEndpoint) []string {
	var workerIds []string
	for _, wep := range arr {
		if wep.Worker != nil {
			workerIds = append(workerIds, wep.Worker.WorkerId)
		} else {
			workerIds = append(workerIds, wep.Ep.Description())
		}
	}
	sort.Strings(workerIds)
	return workerIds
}

// Sync synchronizes the endpoints if resolver's endpoints changed.
func (l *SWRRLoadBalancer) Sync() {
	newEndpoints := l.Resolver.GetWeightEndpoints()

	if SameEndpoints(newEndpoints, l.origEndpoints) {
		return
	}

	klog.V(3).Infof("[%p] new endpoints: %v", l, getWorkerIds(newEndpoints))

	l.origEndpoints = newEndpoints
	var shuffleEndpoints []structs.WeightEndpoint
	shuffleEndpoints = append(shuffleEndpoints, newEndpoints...)
	rand.Shuffle(len(shuffleEndpoints), func(i, j int) {
		shuffleEndpoints[i], shuffleEndpoints[j] = shuffleEndpoints[j], shuffleEndpoints[i]
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = l.items[:0]
	for _, ep := range shuffleEndpoints {
		weighted := &smoothWeighted{
			wep:             ep,
			currentWeight:   0,
			effectiveWeight: ep.Weight,
		}
		l.items = append(l.items, weighted)
	}
}

func (l *SWRRLoadBalancer) GetNextToken() (token *structs.Token, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.items) == 0 {
		err = consts.ErrorEndpointNotFound
		return
	}

	if len(l.items) == 1 {
		return getTokenFromWeightEndpoint(l.items[0].wep), nil
	}

	item := l.nextSmoothWeighted()
	if item == nil {
		err = consts.ErrorEndpointNotFound
		return
	}

	return getTokenFromWeightEndpoint(item.wep), nil
}

func getTokenFromWeightEndpoint(wep structs.WeightEndpoint) *structs.Token {
	token := &structs.Token{
		Endpoint: wep.Ep,
		Count:    1,
	}
	if wep.Worker != nil {
		token.InferMode = wep.Worker.InferMode
		token.InstName = wep.Worker.InstName
		token.WorkerId = wep.Worker.WorkerId
	}
	return token
}

func (l *SWRRLoadBalancer) GetNextTokens(req *structs.Request) (*structs.NextTokens, error) {
	t, err := l.GetNextToken()
	if err != nil {
		return nil, err
	}
	klog.V(3).Infof("[%p] get next token: %s", l, t.String())
	return &structs.NextTokens{Tokens: []structs.Token{*t}, Tokens2: nil}, nil
}

// ReleaseToken not implemented
func (l *SWRRLoadBalancer) ReleaseToken(req *structs.Request, token *structs.Token) {}

func (l *SWRRLoadBalancer) ExcludeService(string) {}

// nextSmoothWeighted return next endpoint using smooth weighted round-robin algorithm
func (l *SWRRLoadBalancer) nextSmoothWeighted() (best *smoothWeighted) {
	total := 0

	for i := 0; i < len(l.items); i++ {
		w := l.items[i]
		if w == nil {
			continue
		}

		w.currentWeight += w.effectiveWeight
		total += w.effectiveWeight
		if w.effectiveWeight < w.wep.Weight {
			w.effectiveWeight++
		}

		if best == nil || w.currentWeight > best.currentWeight {
			best = w
		}
	}

	if best == nil {
		return nil
	}

	best.currentWeight -= total
	return best
}
