package server

import (
	"bytes"
	"testing"
)

func TestOutputBudgetDrainsBothStreams(t *testing.T) {
	budget := &outputBudget{limit: 1024}
	a, b := cappedOutput{budget: budget}, cappedOutput{budget: budget}
	p := bytes.Repeat([]byte("x"), 65536)
	if n, e := a.Write(p); e != nil || n != len(p) {
		t.Fatal("stdout not drained")
	}
	if n, e := b.Write(p); e != nil || n != len(p) {
		t.Fatal("stderr not drained")
	}
	if a.Len()+b.Len() != 1024 || !budget.truncated {
		t.Fatal("combined output unbounded")
	}
}
