package terminal

import (
	"context"
	"errors"
	"io"
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

func pair(t *testing.T) (*websocket.Conn, *websocket.Conn, *observedConn) {
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
	safe := conn
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

type fakeTerminal struct {
	closed    atomic.Int32
	failWrite bool
	final     atomic.Bool
}

func (f *fakeTerminal) Close() error { f.closed.Add(1); return nil }
func (f *fakeTerminal) Read(b []byte) (int, error) {
	if f.final.CompareAndSwap(true, false) {
		return copy(b, []byte("final")), io.EOF
	}
	return 0, io.EOF
}
func (f *fakeTerminal) Write(b []byte) (int, error) {
	if f.failWrite {
		return 0, io.ErrShortWrite
	}
	return len(b), nil
}
func (f *fakeTerminal) Resize(int, int) error { return nil }
func (f *fakeTerminal) Wait() error           { return nil }
func TestRetentionEpochProtectsReattachedPTY(t *testing.T) {
	old, _, _ := pair(t)
	fresh, _, _ := pair(t)
	term := &fakeTerminal{}
	id := "retention-regression"
	s := &terminalSession{id: id, term: term, conn: old}
	sessionsMu.Lock()
	sessions[id] = s
	sessionsMu.Unlock()
	t.Cleanup(func() { closeSession(id, s) })
	s.detach(old)
	s.mu.Lock()
	epoch := s.retentionEpoch
	s.mu.Unlock()
	got, first, err := acquireSession(id, fresh)
	if err != nil || first || got != s {
		t.Fatalf("PTY not reused: %v %v", first, err)
	}
	expireSession(id, s, epoch)
	if term.closed.Load() != 0 {
		t.Fatal("stale retention callback closed recovered PTY")
	}
	s.mu.Lock()
	live := !s.removed && s.conn == fresh
	s.mu.Unlock()
	if !live {
		t.Fatal("reattached session was lost")
	}
}
func TestPTYInputFailureIsNotSilentlyAccepted(t *testing.T) {
	conn, _, _ := pair(t)
	s := &terminalSession{id: "input-failure", term: &fakeTerminal{failWrite: true}, conn: conn}
	sessionsMu.Lock()
	sessions[s.id] = s
	sessionsMu.Unlock()
	t.Cleanup(func() { closeSession(s.id, s) })
	if s.writeInput(conn, []byte("command")) {
		t.Fatal("failed PTY write accepted")
	}
	s.mu.Lock()
	detached := s.conn == nil
	s.mu.Unlock()
	if !detached {
		t.Fatal("failed input did not detach connection")
	}
}
func TestPTYFinalBytesAreForwardedBeforeEOF(t *testing.T) {
	conn, peer, _ := pair(t)
	term := &fakeTerminal{}
	term.final.Store(true)
	s := &terminalSession{id: "final-output", term: term, conn: conn}
	sessionsMu.Lock()
	sessions[s.id] = s
	sessionsMu.Unlock()
	t.Cleanup(func() { closeSession(s.id, s) })
	go s.readOutput()
	peer.SetReadDeadline(time.Now().Add(time.Second))
	_, data, err := peer.ReadMessage()
	if err != nil || string(data) != "final" {
		t.Fatalf("trailing bytes lost: %q %v", data, err)
	}
}
func TestProbeTerminalHeartbeatKeepsQuietConnection(t *testing.T) {
	conn, peer, _ := pair(t)
	stop := keepAlive(conn, 20*time.Millisecond, 150*time.Millisecond, 30*time.Millisecond)
	defer stop()
	done := make(chan error, 1)
	go func() { _, _, e := conn.ReadMessage(); done <- e }()
	go func() {
		for {
			if _, _, e := peer.ReadMessage(); e != nil {
				return
			}
		}
	}()
	select {
	case e := <-done:
		t.Fatalf("quiet connection lost: %v", e)
	case <-time.After(350 * time.Millisecond):
	}
	conn.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader did not stop")
	}
}
func TestProbeTerminalHeartbeatDetectsMissingPong(t *testing.T) {
	conn, peer, _ := pair(t)
	peer.SetPingHandler(func(string) error { return nil })
	go func() {
		for {
			if _, _, e := peer.ReadMessage(); e != nil {
				return
			}
		}
	}()
	stop := keepAlive(conn, 20*time.Millisecond, 100*time.Millisecond, 30*time.Millisecond)
	defer stop()
	_, _, e := conn.ReadMessage()
	var n net.Error
	if !errors.As(e, &n) || !n.Timeout() {
		t.Fatalf("missing Pong not detected: %v", e)
	}
}

func TestRetiredConnectionCannotCloseReattachedPTY(t *testing.T) {
	old, _, _ := pair(t)
	fresh, _, _ := pair(t)
	term := &fakeTerminal{}
	s := &terminalSession{id: "stale-close", term: term, conn: fresh}
	sessionsMu.Lock()
	sessions[s.id] = s
	sessionsMu.Unlock()
	t.Cleanup(func() { closeSession(s.id, s) })
	closeSessionIf(s.id, s, nil, old)
	if term.closed.Load() != 0 {
		t.Fatal("retired input reader destroyed reattached PTY")
	}
}
