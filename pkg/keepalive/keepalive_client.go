package keepalive

import (
	"context"
	"encoding/json"
	"fmt"
	"llumnix/pkg/resolver"
	"llumnix/pkg/types"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"sync"

	"github.com/gorilla/websocket"
	"k8s.io/klog/v2"
)

type KeepAliveClient struct {
	remoteName     string
	remoteToken    string
	remoteResolver resolver.Resolver

	mu            sync.RWMutex
	localEndpoint types.Endpoint
	remoteReady   bool

	remoteMu       sync.RWMutex
	remoteEndpoint types.Endpoint
}

func NewKeepAliveClient(remoteName, remoteToken string, remoteResolver resolver.Resolver) *KeepAliveClient {
	return &KeepAliveClient{
		remoteName:     remoteName,
		remoteToken:    remoteToken,
		remoteResolver: remoteResolver,
	}
}

func (kac *KeepAliveClient) GetLocalEndpoint() types.Endpoint {
	kac.mu.RLock()
	defer kac.mu.RUnlock()
	return kac.localEndpoint
}

func (kac *KeepAliveClient) GetRemoteEndpoint() types.Endpoint {
	kac.remoteMu.RLock()
	defer kac.remoteMu.RUnlock()
	return kac.remoteEndpoint
}

func (kac *KeepAliveClient) IsRemoteReady() bool {
	kac.mu.RLock()
	defer kac.mu.RUnlock()
	return kac.remoteReady
}

func (kac *KeepAliveClient) StartAndAwaitReady() {
	ready := make(chan struct{}, 1)
	go kac.loopCheck(ready)
	<-ready
}

func (kac *KeepAliveClient) tryConnect(address string, accessToken string) (wsConn *websocket.Conn, err error) {
	url := fmt.Sprintf("ws://%s/keepalive", address)
	h := http.Header{}
	if len(accessToken) > 0 {
		h.Add("Authorization", accessToken)
	}

	dialer := websocket.DefaultDialer
	// use ipv4
	dialer.NetDial = func(network, addr string) (net.Conn, error) {
		return net.Dial("tcp4", addr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()

	wsConn, _, err = dialer.DialContext(ctx, url, h)
	if err == nil {
		return
	}
	klog.Warningf("dial %s failed: %v", url, err)
	return
}

// checkPingPong check remoteResolver ping-pong
func (kac *KeepAliveClient) checkPingPong(ctx context.Context, conn *websocket.Conn) {
	var missedPings atomic.Int32

	conn.SetPingHandler(func(appData string) error {
		missedPings.Store(0)
		if err := conn.WriteMessage(websocket.PongMessage, []byte(appData)); err != nil {
			klog.Warningf("Failed to write pong message: %v", err)
		}
		return nil
	})

	ticker := time.NewTicker(3 * time.Second) // send ping every 3 seconds
	defer func() {
		ticker.Stop()
		conn.Close()
	}()
OuterLoop:
	for {
		select {
		case <-ctx.Done():
			break OuterLoop
		case <-ticker.C:
			if missedPings.Load() >= 2 {
				// two pings missed, the connection may be broken
				klog.V(3).Infof("2 pings missed.")
				break OuterLoop
			}
			missedPings.Add(1)
		}
	}
	klog.V(3).Infof("the goroutine that check remoteResolver ping is finished.")
}

func (kac *KeepAliveClient) ensureGetRemoteEndpoint() types.Endpoint {
	errCnt := 0
	for {
		endpoints, _ := kac.remoteResolver.GetEndpoints()
		if len(endpoints) > 0 {
			kac.setRemoteEndpoint(endpoints[0])
			return endpoints[0]
		}
		klog.Infof("wait remoteResolver(%s) ready ...", kac.remoteName)
		errCnt++
		if errCnt > 2 {
			originReady := kac.markRemoteUnready()
			if originReady {
				klog.Warningf("remote remoteResolver(%s) is not ready.", kac.remoteName)
			}
		}
		time.Sleep(3 * time.Second)
	}
}

func (kac *KeepAliveClient) ensureConnected() *websocket.Conn {
	klog.Infof("try connect to remote remoteResolver(%s) ... ", kac.remoteName)
	connectErrCnt := 0
	for {
		remoteEndpoint := kac.ensureGetRemoteEndpoint()
		conn, err := kac.tryConnect(remoteEndpoint.String(), kac.remoteToken)
		if err == nil {
			klog.Infof("keepalive with remove remoteResolver: %s/%s", kac.remoteName, remoteEndpoint.String())
			return conn
		}
		connectErrCnt++
		if connectErrCnt > 2 {
			originReady := kac.markRemoteUnready()
			if originReady {
				klog.Warningf("connect remote remoteResolver(%s), error: %v", kac.remoteName, err)
			}
		}
		time.Sleep(1 * time.Second)
		klog.Warningf("try connect to remote remoteResolver: %s/%s", kac.remoteName, remoteEndpoint.String())
	}
}

func (kac *KeepAliveClient) setLocalEndpoint(ep types.Endpoint) {
	kac.mu.Lock()
	defer kac.mu.Unlock()
	kac.localEndpoint = ep
}
func (kac *KeepAliveClient) setRemoteEndpoint(ep types.Endpoint) {
	kac.remoteMu.Lock()
	defer kac.remoteMu.Unlock()
	kac.remoteEndpoint = ep
}

func (kac *KeepAliveClient) setRemoteReady(localEndpoint types.Endpoint) {
	kac.mu.Lock()
	defer kac.mu.Unlock()
	kac.localEndpoint = localEndpoint
	kac.remoteReady = true
}

func (kac *KeepAliveClient) markRemoteUnready() bool {
	kac.mu.Lock()
	defer kac.mu.Unlock()
	ready := kac.remoteReady
	kac.remoteReady = false
	return ready
}

func (kac *KeepAliveClient) handShake(conn *websocket.Conn) (types.Endpoint, error) {
	var localEndpoint types.Endpoint
	localEndpoint, err := types.NewEndpoint(conn.LocalAddr().String())
	if err != nil {
		klog.Warningf("hand shake: new local endpoint failed: %v", err)
		return localEndpoint, err
	}
	data, _ := json.Marshal(localEndpoint)
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		klog.Warningf("hand shake: send local endpoint to remote failed: %v", err)
		return localEndpoint, err
	}
	_, msg, err := conn.ReadMessage()
	if err != nil || string(msg) != "ok" {
		klog.Warningf("hand shake: read from remote failed: %v, %s", err, string(msg))
		return localEndpoint, err
	}
	return localEndpoint, nil
}

func (kac *KeepAliveClient) loopCheck(ready chan struct{}) {
	first := true
	var conn *websocket.Conn = nil
	for {
		if conn != nil {
			conn.Close()
		}
		conn = kac.ensureConnected()
		localEndpoint, err := kac.handShake(conn)
		if err != nil {
			klog.Warningf("handshake failed: %v", err)
			continue
		}

		kac.setRemoteReady(localEndpoint)

		if first {
			ready <- struct{}{}
			first = false
		}

		ctx, cancel := context.WithCancel(context.Background())
		go kac.checkPingPong(ctx, conn)
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				remoteAddr := kac.GetRemoteEndpoint()
				klog.Warningf("disconnected from remote remoteResolver (%s): %v", remoteAddr.String(), err)
				cancel()
				break
			}
		}
	}
}
