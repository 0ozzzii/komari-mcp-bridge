package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrefixAndBoundedNativeFileTransfer(t *testing.T) {
	content := []byte("中文 binary\x00file")
	steps := []string{}
	var received []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-test-key" {
			t.Fatal("upstream authorization missing")
		}
		if !strings.HasPrefix(r.URL.Path, "/komari/api/") {
			t.Fatalf("prefix or query incorrectly encoded: %s", r.URL)
		}
		if strings.HasSuffix(r.URL.Path, "/download") {
			if r.URL.Query().Get("path") != "/tmp/文件 with spaces" || r.Header.Get("Range") != "bytes=0-255" {
				t.Error("invalid download path/range")
			}
			w.WriteHeader(206)
			w.Write(content)
			return
		}
		operation := r.URL.Query().Get("operation")
		steps = append(steps, operation)
		data := any(map[string]any{"complete": true})
		switch operation {
		case "init":
			var body struct {
				Path      string `json:"path"`
				Size      int    `json:"size"`
				ChunkSize int    `json:"chunk_size"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Path != "/tmp/target" || body.Size != len(content) || body.ChunkSize != len(content) {
				t.Error("bad upload init")
			}
			data = map[string]any{"upload_id": "test-upload"}
		case "chunk":
			if r.URL.Query().Get("upload_id") != "test-upload" || r.ContentLength != int64(len(content)) {
				t.Error("invalid native chunk")
			}
			received, _ = io.ReadAll(r.Body)
		case "merge":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["upload_id"] != "test-upload" {
				t.Error("bad merge")
			}
		default:
			t.Errorf("unexpected operation %q", operation)
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": data})
	}))
	defer server.Close()
	client, _ := New(server.URL+"/komari", "private-test-key")
	read, more, err := client.ReadFile(context.Background(), "node-a", "/tmp/文件 with spaces", 0, 256)
	if err != nil || more || !bytes.Equal(read, content) {
		t.Fatalf("download: %q %v %v", read, more, err)
	}
	if _, err = client.WriteFile(context.Background(), "node-a", "/tmp/target", content); err != nil {
		t.Fatal(err)
	}
	if strings.Join(steps, ",") != "init,chunk,merge" || !bytes.Equal(received, content) {
		t.Fatal("native upload changed exact file bytes")
	}
	if _, err = client.WriteFile(context.Background(), "node-a", "/tmp/target", make([]byte, 131073)); err == nil {
		t.Fatal("write limit not enforced")
	}
}
func TestRedirectDoesNotForwardAdministratorKey(t *testing.T) {
	visited := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { visited = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	client, _ := New(redirect.URL, "test-private-key")
	if _, err := client.RPC(context.Background(), "admin:listClients", map[string]any{}); err == nil {
		t.Fatal("redirect accepted")
	}
	if visited {
		t.Fatal("administrator key crossed redirect boundary")
	}
}
