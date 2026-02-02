package keepalive

import (
	"encoding/json"
	"llumnix/pkg/types"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"k8s.io/klog/v2"
)

type writeMessage struct {
	data      []byte
	needClose bool
}

type KeepAliveServer struct {
	remoteEndpoint types.Endpoint
	wsConn         *websocket.Conn
	msgChan        chan *writeMessage
}

func NewKeepAliveServer(wsConn *websocket.Conn) *KeepAliveServer {
	return &KeepAliveServer{
		wsConn:  wsConn,
		msgChan: make(chan *writeMessage, 100),
	}
}

// writeWithPingPong do ping pong with service or gateway
func (kas *KeepAliveServer) checkPingPongLoop() {
	var missedPongs atomic.Int32
	conn := kas.wsConn
	conn.SetPongHandler(func(appData string) error {
		missedPongs.Store(0)
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
		case message := <-kas.msgChan:
			if err := conn.WriteMessage(websocket.TextMessage, message.data); err != nil {
				break OuterLoop
			}
			if message.needClose {
				break OuterLoop
			}
		case <-ticker.C:
			if missedPongs.Load() >= 2 {
				// two pongs missed, the connection may be broken
				klog.V(3).Infof("2 pongs missed: %v", kas.wsConn.LocalAddr().String())
				break OuterLoop
			}
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				break OuterLoop
			}
			missedPongs.Add(1)
		}
	}
}

func (kas *KeepAliveServer) HandShake() (types.Endpoint, error) {
	var remoteEndpoint types.Endpoint
	// Read the first message from the WebSocket to obtain the address of the connected gateway
	// this address can be used to record the token usage associated with this gateway.
	_, data, err := kas.wsConn.ReadMessage()
	if err != nil {
		klog.Warningf("handle shake: could not read from remote: %v", err)
		return remoteEndpoint, err
	}
	if err := json.Unmarshal(data, &remoteEndpoint); err != nil {
		klog.Warningf("handle shake: could not get remote endpoint from gateway: %v", err)
		return remoteEndpoint, err
	}
	if err := kas.wsConn.WriteMessage(websocket.TextMessage, []byte("ok")); err != nil {
		klog.Warningf("handle shake: write response to gateway: %v", err)
		return remoteEndpoint, err
	}

	go func() {
		for {
			_, _, err := kas.wsConn.ReadMessage()
			if err != nil {
				// abnormal TCP disconnection, received RST..., faster than pingpong check
				klog.Infof("disconnected with gateway %s: %v", remoteEndpoint.String(), err)
				kas.wsConn.Close()
				kas.msgChan <- &writeMessage{data: []byte("closed"), needClose: true}
				return
			}
		}
	}()

	kas.remoteEndpoint = remoteEndpoint
	return remoteEndpoint, nil
}

func (kas *KeepAliveServer) StartKeepAlive(callback func()) {
	klog.Infof("start keep alive with remote endpoint: %s", kas.remoteEndpoint.String())
	kas.checkPingPongLoop()
	callback()
	klog.Infof("end keepalive with remote: %s", kas.remoteEndpoint.String())
}
