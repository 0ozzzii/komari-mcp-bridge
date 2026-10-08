package terminal

import (
	"bytes"
	"strings"
)

// Parse raw bytes before UTF-8 decoding or ANSI display processing. Marker bytes
// are emitted by shell functions at runtime and absent from echoed input source.
type parser struct {
	prefix []byte
	tail   []byte
	end    byte // zero keeps the original POSIX RS/US framing
}

func (p *parser) feed(data []byte, output func([]byte), event func([]string)) {
	b := append(p.tail, data...)
	p.tail = nil
	for len(b) > 0 {
		index := bytes.Index(b, p.prefix)
		if index < 0 {
			keep := 0
			for n := min(len(b), len(p.prefix)-1); n > 0; n-- {
				if bytes.Equal(b[len(b)-n:], p.prefix[:n]) {
					keep = n
					break
				}
			}
			output(b[:len(b)-keep])
			p.tail = append(p.tail, b[len(b)-keep:]...)
			return
		}
		if index > 0 {
			output(b[:index])
			b = b[index:]
		}
		terminator := p.end
		if terminator == 0 {
			terminator = 0x1f
		}
		end := bytes.IndexByte(b[len(p.prefix):], terminator)
		if end >= 0 {
			end += len(p.prefix)
		}
		if end < 0 {
			if len(b) > 512 {
				output(b[:1])
				b = b[1:]
				continue
			}
			p.tail = append(p.tail, b...)
			return
		}
		event(strings.Split(string(b[len(p.prefix):end]), ":"))
		b = b[end+1:]
	}
}
