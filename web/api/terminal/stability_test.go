package terminal

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
	"github.com/komari-monitor/komari/web/connection"
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

func pair(t *testing.T) (*connection.SafeConn, *websocket.Conn, *observedConn) {
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
	safe := connection.NewSafeConn(conn)
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

func TestReplacementRetiresOldReadersAndForwardsNewPair(t *testing.T) {
	oldBrowser, _, _ := pair(t)
	newBrowser, newPeer, _ := pair(t)
	agent, _, socket := pair(t)
	socket.observe.Store(true)
	id := "replacement-regression"
	TerminalSessionsMutex.Lock()
	TerminalSessions[id] = &TerminalSession{UUID: "node", UserUUID: "caller", RequesterIp: "127.0.0.1", Browser: oldBrowser, Agent: agent}
	TerminalSessionsMutex.Unlock()
	t.Cleanup(func() { closeSession(id) })
	done := make(chan struct{})
	noop := func(string, string, string, string) {}
	go func() { forwardTerminal(id, oldBrowser, agent, noop); close(done) }()
	receive(t, socket.entered)
	if _, ok := attachBrowser(id, "caller", false, newBrowser); !ok {
		t.Fatal("reattach rejected")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("old readers did not exit")
	}
	if n := socket.reads.Load(); n != 0 {
		t.Fatalf("old agent readers still active: %d", n)
	}
	fresh, peer, _ := pair(t)
	if _, ok := attachAgent(id, fresh); !ok {
		t.Fatal("agent reattach rejected")
	}
	done2 := make(chan struct{})
	go func() { forwardTerminal(id, newBrowser, fresh, noop); close(done2) }()
	if err := peer.WriteMessage(websocket.BinaryMessage, []byte("restored")); err != nil {
		t.Fatal(err)
	}
	newPeer.SetReadDeadline(time.Now().Add(time.Second))
	_, data, err := newPeer.ReadMessage()
	if err != nil || string(data) != "restored" {
		t.Fatalf("output not relayed: %q %v", data, err)
	}
	closeSession(id)
	select {
	case <-done2:
	case <-time.After(time.Second):
		t.Fatal("new readers did not exit")
	}
}
func TestTerminalHeartbeatKeepsQuietConnection(t *testing.T) {
	conn, peer, _ := pair(t)
	stop := keepAlive(conn, 20*time.Millisecond, 150*time.Millisecond, 30*time.Millisecond)
	defer stop()
	done := make(chan error, 1)
	go func() { _, _, err := conn.ReadMessage(); done <- err }()
	go func() {
		for {
			if _, _, err := peer.ReadMessage(); err != nil {
				return
			}
		}
	}()
	select {
	case err := <-done:
		t.Fatalf("quiet connection expired despite Pongs: %v", err)
	case <-time.After(350 * time.Millisecond):
	}
	conn.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader did not stop")
	}
}
func TestTerminalHeartbeatDetectsMissingPong(t *testing.T) {
	conn, peer, _ := pair(t)
	peer.SetPingHandler(func(string) error { return nil })
	go func() {
		for {
			if _, _, err := peer.ReadMessage(); err != nil {
				return
			}
		}
	}()
	stop := keepAlive(conn, 20*time.Millisecond, 100*time.Millisecond, 30*time.Millisecond)
	defer stop()
	_, _, err := conn.ReadMessage()
	var n net.Error
	if !errors.As(err, &n) || !n.Timeout() {
		t.Fatalf("missing Pong did not time out: %v", err)
	}
}

func TestStaleForwarderCloseCannotDestroyReplacement(t *testing.T) {
	oldBrowser, _, _ := pair(t)
	oldAgent, _, _ := pair(t)
	browser, _, _ := pair(t)
	agent, _, _ := pair(t)
	id := "stale-close"
	TerminalSessionsMutex.Lock()
	TerminalSessions[id] = &TerminalSession{Browser: browser, Agent: agent}
	TerminalSessionsMutex.Unlock()
	t.Cleanup(func() { closeSession(id) })
	closeSessionIfOwned(id, oldBrowser, oldAgent)
	TerminalSessionsMutex.Lock()
	live := TerminalSessions[id] != nil && TerminalSessions[id].Browser == browser
	TerminalSessionsMutex.Unlock()
	if !live {
		t.Fatal("retired forwarder closed new session")
	}
}
