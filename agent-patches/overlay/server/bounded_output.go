package server

import (
	"bytes"
	"sync"
)

type outputBudget struct {
	mu          sync.Mutex
	limit, used int
	truncated   bool
}
type cappedOutput struct {
	budget *outputBudget
	buf    bytes.Buffer
}

// Always report consuming the full write: discard excess while draining so
// a verbose child is not blocked by an artificial full pipe.
func (w *cappedOutput) Write(p []byte) (int, error) {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	n := min(len(p), max(0, w.budget.limit-w.budget.used))
	w.buf.Write(p[:n])
	w.budget.used += n
	if n < len(p) {
		w.budget.truncated = true
	}
	return len(p), nil
}
func (w *cappedOutput) String() string {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	return w.buf.String()
}
func (w *cappedOutput) Len() int { w.budget.mu.Lock(); defer w.budget.mu.Unlock(); return w.buf.Len() }
