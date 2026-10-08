package terminal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestBootstrapRetryHandlesColdInputAndStopsOnReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	calls := 0
	err := retryBootstrap(ctx, time.Millisecond, func() (bool, error) {
		calls++
		// Model two writes discarded before the shell accepts terminal input;
		// the third produces READY and no fourth write is permitted.
		return calls < 3, nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("cold initialization: calls=%d err=%v", calls, err)
	}
}

func TestWindowsBootstrapStopsBeforeCommandsOrChangedConnections(t *testing.T) {
	for _, stop := range []string{"ready", "changed-connection", "active-command"} {
		t.Run(stop, func(t *testing.T) {
			frames := make(chan string, 8)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer c.Close()
				for {
					_, data, err := c.ReadMessage()
					if err != nil {
						return
					}
					frames <- string(data)
				}
			}))
			defer server.Close()
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &Session{ctx: ctx, conn: conn, manager: &Manager{readyTimeout: 3 * time.Second}, Record: Record{State: "waiting_agent"}}
			done := make(chan struct{})
			go func() { s.bootstrapWindows(conn, []byte("initializer-only")); close(done) }()
			select {
			case frame := <-frames:
				if frame != "initializer-only" {
					t.Fatal(frame)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("initial probe not sent")
			}
			s.mu.Lock()
			switch stop {
			case "ready":
				s.Verified = true
				s.State = "ready"
			case "changed-connection":
				s.conn = nil
			case "active-command":
				s.Active = "business-command"
			}
			s.mu.Unlock()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("bootstrap worker did not stop")
			}
			select {
			case frame := <-frames:
				t.Fatalf("late initializer after %s: %q", stop, frame)
			default:
			}
		})
	}
}

func TestLateReadyCannotReverifyResumedOrBusyContext(t *testing.T) {
	for _, tc := range []struct {
		state, active string
		awaiting      bool
	}{
		{"reconnecting", "", false}, {"waiting_agent", "", false}, {"ready", "business-command", true},
	} {
		m := &Manager{dir: t.TempDir()}
		s := &Session{ctx: context.Background(), manager: m, changed: make(chan struct{}), awaitBootstrap: tc.awaiting, Record: Record{ID: "late-ready", Nonce: "nonce", PID: "original", State: tc.state, Active: tc.active, Commands: map[string]*Command{}}}
		prefix, end := shellFraming(ShellPowerShell, "nonce")
		p := parser{prefix: prefix, end: end}
		s.consume(&p, []byte("~KMB:nonce:READY:replacement~"))
		if s.PID != "original" || s.State != tc.state || s.Verified {
			t.Fatal("late READY changed context", s.Record)
		}
	}
}

func TestBootstrapRetryIsBoundedAndWriteFailuresStop(t *testing.T) {
	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		calls := 0
		err := retryBootstrap(ctx, time.Millisecond, func() (bool, error) { calls++; return true, nil })
		if !errors.Is(err, context.DeadlineExceeded) || calls == 0 {
			t.Fatalf("deadline not enforced: calls=%d err=%v", calls, err)
		}
	})
	t.Run("write-error", func(t *testing.T) {
		want := errors.New("lost transport")
		calls := 0
		err := retryBootstrap(context.Background(), time.Millisecond, func() (bool, error) { calls++; return true, want })
		if !errors.Is(err, want) || calls != 1 {
			t.Fatalf("write error retried: calls=%d err=%v", calls, err)
		}
	})
}
