package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Exercise the real monitoring dialer/handlers using only loopback networking.
// Shorten the initial deadline; a received control frame must extend it.
func TestMonitorControlFramesRefreshReadDeadline(t *testing.T) {
	for _, kind := range []int{websocket.PingMessage, websocket.PongMessage} {
		t.Run(map[int]string{websocket.PingMessage: "ping", websocket.PongMessage: "pong"}[kind], func(t *testing.T) {
			start := make(chan struct{})
			upgrader := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				peer, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer peer.Close()
				<-start
				if err := peer.WriteControl(kind, nil, time.Now().Add(time.Second)); err != nil {
					return
				}
				time.Sleep(180 * time.Millisecond)
				_ = peer.WriteMessage(websocket.TextMessage, []byte("still-connected"))
			}))
			defer server.Close()
			conn, err := connectWebSocket("ws" + strings.TrimPrefix(server.URL, "http"))
			if err != nil {
				close(start)
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
			close(start)
			_, data, err := conn.ReadMessage()
			if err != nil || string(data) != "still-connected" {
				t.Fatalf("monitor control frame did not refresh deadline: %q %v", data, err)
			}
		})
	}
}
