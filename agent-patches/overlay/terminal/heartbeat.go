package terminal

import (
	"github.com/gorilla/websocket"
	"sync"
	"time"
)

const terminalPingInterval = 20 * time.Second
const terminalReadWait = 75 * time.Second
const terminalWriteWait = 5 * time.Second

func keepAlive(conn *websocket.Conn, interval, readWait, writeWait time.Duration) func() {
	conn.SetReadLimit(1 << 20)
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
