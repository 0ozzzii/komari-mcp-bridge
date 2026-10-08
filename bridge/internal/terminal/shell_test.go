package terminal

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestMarkerFramingAcrossEveryByteAndEcho(t *testing.T) {
	for _, kind := range []ShellType{ShellPOSIX, ShellPowerShell} {
		prefix, end := shellFraming(kind, "nonce")
		p := parser{prefix: prefix, end: end}
		frame := append(append(append([]byte(nil), prefix...), []byte("END:id:7")...), end)
		data := append([]byte(shellBootstrap(kind, "nonce")), []byte("before")...)
		data = append(data, frame...)
		data = append(data, []byte("after")...)
		var out bytes.Buffer
		count := 0
		for _, b := range data {
			p.feed([]byte{b}, func(v []byte) { out.Write(v) }, func(v []string) {
				count++
				if len(v) != 3 || v[0] != "END" || v[1] != "id" || v[2] != "7" {
					t.Fatal(v)
				}
			})
		}
		if count != 1 || !bytes.Equal(out.Bytes(), append(append([]byte(shellBootstrap(kind, "nonce")), []byte("before")...), []byte("after")...)) {
			t.Fatal("echo or split frame misparsed", kind, count, out.String())
		}
	}
}

func TestShellAdaptersPreservePayloadAndAvoidEchoMarkers(t *testing.T) {
	text := strings.Repeat("中文 'quotes' $variable\n", 200)
	lines := shellCommand(ShellPowerShell, "nonce", "id", text)
	var payload strings.Builder
	for _, line := range lines[1 : len(lines)-1] {
		if len(line) > 700 || !strings.HasSuffix(line, "\r") {
			t.Fatal("unbounded or incorrect terminal input")
		}
		chunk := strings.TrimSuffix(strings.TrimPrefix(line, "$global:__kmb_payload += '"), "'\r")
		payload.WriteString(chunk)
	}
	raw, err := base64.StdEncoding.DecodeString(payload.String())
	if err != nil || string(raw) != text+"\n$global:__kmb_command_ok=$?\n" {
		t.Fatal("UTF-8/multiline payload changed")
	}
	for _, line := range append(lines, shellBootstrap(ShellPowerShell, "nonce"), shellResume(ShellPowerShell, "nonce")) {
		if strings.ContainsAny(line, "\x1e\x1f") {
			t.Fatal("input echo can forge marker")
		}
	}
	if strings.Contains(lines[len(lines)-1], "& ") || !strings.Contains(lines[len(lines)-1], ". ([ScriptBlock]") {
		t.Fatal("command not sourced in parent scope")
	}
}

func TestNativeExitCodeBoundaries(t *testing.T) {
	for _, rc := range []int{-2147483648, -1, 0, 255, 3010, 2147483647} {
		if !validShellExit(ShellPowerShell, rc) {
			t.Fatal("Windows exit code rejected", rc)
		}
	}
	for _, rc := range []int{-1, 256, 3010} {
		if validShellExit(ShellPOSIX, rc) {
			t.Fatal("invalid POSIX code accepted", rc)
		}
	}
	if shellForOS("Microsoft Windows 10 Enterprise") != ShellPowerShell || shellForOS("Ubuntu 24.04") != ShellPOSIX {
		t.Fatal("wrong shell adapter")
	}
}
