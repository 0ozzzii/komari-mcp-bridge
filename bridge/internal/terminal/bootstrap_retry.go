package terminal

import (
	"context"
	"time"

	"github.com/gorilla/websocket"
)

// retryBootstrap is strictly for a fresh Windows shell's idempotent settings
// and READY probe, never user commands, staging, input or reconnect recovery.
// send returns false once readiness or a connection/state change ends the wait.
func retryBootstrap(ctx context.Context, interval time.Duration, send func() (bool, error)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		keepWaiting, err := send()
		if err != nil || !keepWaiting {
			return err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Session) bootstrapWindows(conn *websocket.Conn, data []byte) {
	ctx, cancel := context.WithTimeout(s.ctx, s.manager.readyTimeout)
	defer cancel()
	err := retryBootstrap(ctx, time.Second, func() (bool, error) {
		// Use the same serialization as ordinary dispatch and pin every write
		// to the original connection. A late retry cannot enter a new shell or
		// follow an admitted command after READY.
		s.op.Lock()
		defer s.op.Unlock()
		s.write.Lock()
		defer s.write.Unlock()
		s.mu.Lock()
		waiting := ctx.Err() == nil && s.conn == conn && s.State == "waiting_agent" && !s.Verified && s.Active == ""
		s.mu.Unlock()
		if !waiting {
			return false, nil
		}
		if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return false, err
		}
		return true, conn.WriteMessage(websocket.BinaryMessage, data)
	})
	if err != nil && ctx.Err() == nil {
		// Closing only this connection lets the existing reader record the
		// transport failure and uncertainty. Never replay a user command.
		conn.Close()
	}
}
