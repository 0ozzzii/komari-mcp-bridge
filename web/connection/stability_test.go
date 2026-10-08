package connection

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Observe concurrent reads, and deterministically model a stalled socket write.
// This is a local fault fixture, not a production network measurement.
type observedConn struct {
	net.Conn
	observe    atomic.Bool
	blockWrite atomic.Bool
	reads      atomic.Int32
	entered    chan int32
	writing    chan struct{}
	release    chan struct{}
	closed     chan struct{}
	once       sync.Once
}

func (c *observedConn) Read(b []byte) (int, error) {
	if c.observe.Load() {
		n := c.reads.Add(1)
		defer c.reads.Add(-1)
		select {
		case c.entered <- n:
		default:
		}
	}
	return c.Conn.Read(b)
}
func (c *observedConn) Write(b []byte) (int, error) {
	if c.blockWrite.Load() {
		select {
		case c.writing <- struct{}{}:
		default:
		}
		select {
		case <-c.release:
		case <-c.closed:
			return 0, net.ErrClosed
		}
	}
	return c.Conn.Write(b)
}
func (c *observedConn) Close() error { c.once.Do(func() { close(c.closed) }); return c.Conn.Close() }

func pair(t *testing.T) (*SafeConn, *websocket.Conn, *observedConn) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	got := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			got <- conn
		}
	}))
	var socket *observedConn
	dialer := websocket.Dialer{NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		socket = &observedConn{Conn: conn, entered: make(chan int32, 16), writing: make(chan struct{}, 4), release: make(chan struct{}), closed: make(chan struct{})}
		return socket, nil
	}}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	peer := <-got
	safe := NewSafeConn(conn)
	t.Cleanup(func() { socket.Close(); peer.Close(); safe.Close(); server.Close() })
	return safe, peer, socket
}
func receive(t *testing.T, ch <-chan int32) int32 {
	t.Helper()
	select {
	case n := <-ch:
		return n
	case <-time.After(time.Second):
		t.Fatal("fixture read did not start")
		return 0
	}
}

func TestSafeConnCloseInterruptsBlockedWriter(t *testing.T) {
	safe, _, socket := pair(t)
	socket.blockWrite.Store(true)
	writeDone := make(chan error, 1)
	go func() { writeDone <- safe.WriteMessage(websocket.BinaryMessage, []byte("output")) }()
	select {
	case <-socket.writing:
	case <-time.After(time.Second):
		t.Fatal("write not held")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- safe.Close() }()
	select {
	case <-closeDone:
	case <-time.After(200 * time.Millisecond):
		socket.Close()
		t.Fatal("Close blocked behind writer")
	}
	if err := <-writeDone; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("unexpected write result: %v", err)
	}
}
