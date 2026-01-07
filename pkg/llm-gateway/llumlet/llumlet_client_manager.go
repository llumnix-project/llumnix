package llumlet

import (
	"container/list"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/klog/v2"
)

const (
	DefaultPoolSize = -1
)

type ClientManager struct {
	clientConnections          map[string]*ClientConnection
	clientConnectionRequestCnt map[string]int
	maxSize                    int
	mu                         sync.RWMutex
	lruList                    *list.List
}

type ClientConnection struct {
	LlumletClient LlumletClient
	Conn          *grpc.ClientConn
	instanceId    string
	address       string
	ele           *list.Element
	lastUsedTime  time.Time
}

func NewClientConnection(client LlumletClient, conn *grpc.ClientConn, instanceId string, address string) *ClientConnection {
	return &ClientConnection{
		LlumletClient: client,
		Conn:          conn,
		instanceId:    instanceId,
		address:       address,
		lastUsedTime:  time.Now(),
	}
}

func NewClientManager(maxSize int) *ClientManager {
	if maxSize <= 0 {
		maxSize = DefaultPoolSize
	}
	return &ClientManager{
		clientConnections:          make(map[string]*ClientConnection),
		clientConnectionRequestCnt: make(map[string]int),
		maxSize:                    maxSize,
		mu:                         sync.RWMutex{},
		lruList:                    list.New(),
	}
}

func (cm *ClientManager) DecrClientConnectionRequestCnt(instanceId string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cnt, exist := cm.clientConnectionRequestCnt[instanceId]; exist {
		if cnt > 0 {
			cm.clientConnectionRequestCnt[instanceId]--
		}
	}
}

func (cm *ClientManager) GetOrCreateClient(instanceId string, address string) (*ClientConnection, error) {
	client := cm.GetClient(instanceId)
	if client != nil {
		return client, nil
	}

	return cm.createClient(instanceId, address)
}

func (cm *ClientManager) GetClient(instanceId string) *ClientConnection {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if clientConnection, exist := cm.clientConnections[instanceId]; exist {
		cm.lruList.MoveToFront(clientConnection.ele)
		cm.clientConnectionRequestCnt[instanceId]++
		clientConnection.lastUsedTime = time.Now()
		return clientConnection
	}
	return nil
}

func (cm *ClientManager) createClient(instanceId string, address string) (*ClientConnection, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cc, exists := cm.clientConnections[instanceId]; exists {
		// client is created during waiting mu.Lock()
		cc.lastUsedTime = time.Now()
		cm.lruList.MoveToFront(cc.ele)
		cm.clientConnectionRequestCnt[instanceId]++
		return cc, nil
	}

	// reach max size
	for cm.maxSize > 0 && len(cm.clientConnections) >= cm.maxSize {
		lastEle := cm.lruList.Back()
		if lastEle != nil {
			lastClientConnection := lastEle.Value.(*ClientConnection)
			lastInstanceId := lastClientConnection.instanceId
			if cm.clientConnectionRequestCnt[lastInstanceId] > 0 {
				// last clientConnection is not allow to be removed, reject create New Client
				return nil, fmt.Errorf("create llumlet llumletClient failed, reach max size")
			}
			if err := lastClientConnection.Conn.Close(); err != nil {
				klog.Errorf("Error closing connection to %s: %v", lastClientConnection.address, err)
			}
			delete(cm.clientConnections, lastInstanceId)
			delete(cm.clientConnectionRequestCnt, lastInstanceId)
			cm.lruList.Remove(lastEle)
		}
	}

	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		klog.Errorf("Error create llumnix gprc connection to %s: %v", address, err)
		return nil, err
	}

	llumnixClient := NewLlumletClient(conn)

	clientConn := NewClientConnection(llumnixClient, conn, instanceId, address)

	ele := cm.lruList.PushFront(clientConn)
	clientConn.ele = ele

	cm.clientConnections[instanceId] = clientConn
	cm.clientConnectionRequestCnt[instanceId] = 1
	return clientConn, nil
}

func (cm *ClientManager) CloseAll() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	klog.Infof("Close all llumlet llumletClient connections")
	for _, clientConnection := range cm.clientConnections {
		if err := clientConnection.Conn.Close(); err != nil {
			klog.Errorf("Error closing connection to %s: %v", clientConnection.address, err)
		}
	}
	cm.lruList = list.New()
	cm.clientConnections = make(map[string]*ClientConnection)
	cm.clientConnectionRequestCnt = make(map[string]int)
}
