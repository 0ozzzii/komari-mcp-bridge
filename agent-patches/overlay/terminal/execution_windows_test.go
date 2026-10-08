//go:build windows

package terminal

import "testing"

func TestWindowsNativeCompletionDisarmsDeadline(t *testing.T) {
	for _, code := range []string{"0", "7", "3010", "-1", "-2147483648", "2147483647"} {
		s := &terminalSession{term: &deadlineTerm{}}
		if _, err := s.armDeadline("native", "nonce", 10000, false); err != nil {
			t.Fatal(err)
		}
		for _, b := range []byte("~KMB:nonce:END:native:" + code + "~") {
			s.observeCompletion([]byte{b})
		}
		s.deadline.mu.Lock()
		active := s.deadline.id
		s.deadline.mu.Unlock()
		s.stopDeadline()
		if active != "" {
			t.Fatal("native completed command retained its deadline", code)
		}
	}
	for _, code := range []string{"2147483648", "-2147483649", "invalid"} {
		s := &terminalSession{term: &deadlineTerm{}}
		if _, err := s.armDeadline("native", "nonce", 10000, false); err != nil {
			t.Fatal(err)
		}
		s.observeCompletion([]byte("~KMB:nonce:END:native:" + code + "~"))
		s.deadline.mu.Lock()
		active := s.deadline.id
		s.deadline.mu.Unlock()
		s.stopDeadline()
		if active == "" {
			t.Fatal("invalid completion disarmed deadline", code)
		}
	}
}
