package utils

import (
	"runtime/debug"
	"sync"
	"sync/atomic"

	"k8s.io/klog/v2"
)

// queue item
type Item interface{}

// handle item function
type ItemConsumerHandler func(Item, *ProxyQueue)

// Queue interface
type Queue interface {
	Enqueue(item Item) bool

	EnqueuePriority(item Item) bool

	WaitDequeue() (item Item)

	Length() int
}

// ProxyQueue stores items that satisfy the BaseItem interface.
type ProxyQueue struct {
	model string

	mu       sync.Mutex
	capacity int
	size     int

	normalChannel   chan Item
	priorityChannel chan Item
	busyWorkers     atomic.Int32
	waitSchedulers  atomic.Int32

	started bool
}

func NewProxyQueue(m string, s int) *ProxyQueue {
	return &ProxyQueue{
		model:           m,
		started:         false,
		capacity:        s,
		normalChannel:   make(chan Item, s),
		priorityChannel: make(chan Item, s),
	}
}

func (pq *ProxyQueue) BusyWorkers() int {
	return int(pq.busyWorkers.Load())
}

func (pq *ProxyQueue) AddBusyWorker(val int) {
	pq.busyWorkers.Add(int32(val))
}

func (pq *ProxyQueue) WaitSchedules() int {
	return int(pq.waitSchedulers.Load())
}

func (pq *ProxyQueue) AddWaitScheduler(val int) {
	pq.waitSchedulers.Add(int32(val))
}

func (pq *ProxyQueue) Enqueue(item Item) bool {
	pq.mu.Lock()
	if pq.size >= pq.capacity {
		pq.mu.Unlock()
		return false
	}
	pq.size++
	pq.mu.Unlock()

	pq.normalChannel <- item
	return true
}

func (pq *ProxyQueue) EnqueuePriority(item Item) bool {
	select {
	case pq.priorityChannel <- item:
		pq.mu.Lock()
		pq.size++
		pq.mu.Unlock()
		return true
	default:
		return false
	}
}

// WaitDequeue returns the next item from the queues, prioritizing the priority channel items.
func (pq *ProxyQueue) WaitDequeue() (item Item) {
	select {
	case item = <-pq.priorityChannel: // Return the item from the priority queue if available.
	default:
		// If the priority queue is empty, wait for items in either of the queues.
		select {
		case item = <-pq.priorityChannel: // Double-check the priority queue before processing the regular queue.
		case item = <-pq.normalChannel:
		}
	}
	pq.mu.Lock()
	pq.size--
	pq.mu.Unlock()
	return
}

// Dequeue returns the next item from the queues, prioritizing the priority channel items.
func (pq *ProxyQueue) Length() int {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	return pq.size
}

func (pq *ProxyQueue) waitQueueFunc(handler ItemConsumerHandler) {
	defer func() {
		if e := recover(); e != nil {
			klog.Warningf("wait queue crashed , err: %s\ntrace:%s", e, string(debug.Stack()))
			go pq.waitQueueFunc(handler)
		}
	}()

	for {
		req := pq.WaitDequeue()
		handler(req, pq)
	}
}

func (pq *ProxyQueue) StartHandler(currency int, handler ItemConsumerHandler) {
	if pq.started {
		return
	}

	pq.started = true
	for i := 0; i < currency; i++ {
		go pq.waitQueueFunc(handler)
	}
}

type MultiModelProxyQueue struct {
	serverlessMode bool
	defaultSize    int
	currency       int
	handler        ItemConsumerHandler
	mu             sync.Mutex
	queueMap       map[string]*ProxyQueue
}

func NewMultiModelProxyQueue(serverlessMode bool, cap int, consumerCurrency int, handler ItemConsumerHandler) *MultiModelProxyQueue {
	return &MultiModelProxyQueue{
		serverlessMode: serverlessMode,
		defaultSize:    cap,
		currency:       consumerCurrency,
		handler:        handler,
		queueMap:       make(map[string]*ProxyQueue),
	}
}

func (mpq *MultiModelProxyQueue) GetOrCreateModelProxyQueue(model string) *ProxyQueue {
	if !mpq.serverlessMode {
		model = ""
	}
	pq, new := func() (*ProxyQueue, bool) {
		mpq.mu.Lock()
		defer mpq.mu.Unlock()
		q, ok := mpq.queueMap[model]
		if ok {
			return q, false
		} else {
			newQ := NewProxyQueue(model, mpq.defaultSize)
			mpq.queueMap[model] = newQ
			return newQ, true
		}
	}()
	if new {
		pq.StartHandler(mpq.currency, mpq.handler)
		klog.Infof("start model[%v] queue read handlers with %v concurrency.", model, mpq.currency)
	}
	return pq
}

func (mpq *MultiModelProxyQueue) GetModelProxyQueue() map[string]*ProxyQueue {
	mpq.mu.Lock()
	defer mpq.mu.Unlock()

	newMap := make(map[string]*ProxyQueue)
	for k, v := range mpq.queueMap {
		newMap[k] = v
	}
	return newMap
}

func (mpq *MultiModelProxyQueue) Length() int {
	length := 0
	mpq.mu.Lock()
	defer mpq.mu.Unlock()
	for _, q := range mpq.queueMap {
		length += q.Length()
	}
	return length
}
