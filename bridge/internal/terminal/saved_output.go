package terminal

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type SavedOutputPage struct {
	Output    string `json:"output"`
	Next      int64  `json:"next_offset"`
	More      bool   `json:"has_more"`
	Size      int64  `json:"saved_bytes"`
	Truncated bool   `json:"output_log_truncated"`
	Gap       bool   `json:"output_gap"`
	Replaced  bool   `json:"invalid_utf8_replaced"`
}

// SavedOutput is for the private administrator surface, not normal caller
// tools. It reads a bounded piece of already received output, with byte offsets.
// A session log is a merged PTY stream, not an independent per-command log.
func (m *Manager) SavedOutput(id string, offset int64, limit int) (SavedOutputPage, error) {
	p := SavedOutputPage{}
	if !validID.MatchString(id) || offset < 0 || limit < 256 || limit > 32768 {
		return p, errors.New("invalid output query")
	}
	m.mu.Lock()
	s := m.sessions[id]
	m.mu.Unlock()
	if s == nil {
		return p, errors.New("unknown session")
	}
	s.mu.Lock()
	p.Truncated = s.LogTruncated
	p.Gap = s.Gap
	s.mu.Unlock()
	path := filepath.Join(m.dir, id+".log")
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return p, errors.New("saved output unavailable")
	}
	f, err := os.Open(path)
	if err != nil {
		return p, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !os.SameFile(before, info) || info.Size() > 8<<20 {
		return p, errors.New("invalid output file")
	}
	p.Size = info.Size()
	if offset > p.Size {
		return p, errors.New("output offset beyond saved log")
	}
	length := min(int64(limit), p.Size-offset)
	data := make([]byte, length)
	n, err := f.ReadAt(data, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return p, err
	}
	data = data[:n]
	if offset+int64(n) < p.Size {
		// Keep incomplete UTF-8 suffix bytes for the next read; do not damage
		// characters that happen to span tool/page boundaries.
		for i := max(0, len(data)-3); i < len(data); i++ {
			if utf8.RuneStart(data[i]) && !utf8.FullRune(data[i:]) {
				data = data[:i]
				break
			}
		}
	}
	p.Replaced = !utf8.Valid(data)
	p.Output = strings.ToValidUTF8(string(data), "�")
	p.Next = offset + int64(len(data))
	p.More = p.Next < p.Size
	return p, nil
}
