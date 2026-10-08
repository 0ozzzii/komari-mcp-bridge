package terminal

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

type deadlineTerm struct{ interrupts atomic.Int32 }

func (t *deadlineTerm) Close() error                { return nil }
func (t *deadlineTerm) Read([]byte) (int, error)    { return 0, nil }
func (t *deadlineTerm) Write(p []byte) (int, error) { return len(p), nil }
func (t *deadlineTerm) Resize(int, int) error       { return nil }
func (t *deadlineTerm) Wait() error                 { return nil }
func (t *deadlineTerm) Interrupt() error            { t.interrupts.Add(1); return nil }
func TestCompletionDisarmsDeadlineEvenWithoutConnection(t *testing.T) {
	term := &deadlineTerm{}
	s := &terminalSession{term: term}
	defer s.stopDeadline()
	if _, e := s.armDeadline("one", "randomnonce", 100, false); e != nil {
		t.Fatal(e)
	}
	marker := []byte("\x1eKMB:randomnonce:END:one:7\x1f")
	if runtime.GOOS == "windows" {
		marker = []byte("~KMB:randomnonce:END:one:7~")
	}
	for _, b := range marker {
		s.observeCompletion([]byte{b})
	}
	time.Sleep(130 * time.Millisecond)
	if term.interrupts.Load() != 0 {
		t.Fatal("completed remote command interrupted after losing connection")
	}
	if _, e := s.armDeadline("two", "randomnonce", 40, false); e != nil {
		t.Fatal(e)
	}
	time.Sleep(80 * time.Millisecond)
	if term.interrupts.Load() != 1 {
		t.Fatal("remote deadline depended on connection")
	}
	if _, e := s.armDeadline("three", "randomnonce", 40, false); e == nil {
		t.Fatal("uncertain active command replaced")
	}
}
