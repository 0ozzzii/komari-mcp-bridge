package terminal

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/web/connection"
)

const terminalPingInterval = 20 * time.Second
const terminalReadWait = 75 * time.Second
const terminalWriteWait = 5 * time.Second

// Each network half has its own heartbeat. Control frames are consumed by the
// WebSocket layer and never forwarded as PTY input or shown as terminal output.
func keepAlive(conn *connection.SafeConn, interval, readWait, writeWait time.Duration) func() {
	conn.SetWriteTimeout(writeWait)
	_ = conn.SetReadDeadline(time.Now().Add(readWait))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(readWait)) })
	conn.SetPingHandler(func(data string) error {
		if err := conn.SetReadDeadline(time.Now().Add(readWait)); err != nil {
			return err
		}
		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(writeWait))
	})
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)); err != nil {
					_ = conn.Close()
					return
				}
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}
