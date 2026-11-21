package client

import (
	"bytes"
	"compress/gzip"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/komari-monitor/komari/web/api"
)

func TestClientV2ReportLimits(t *testing.T) {
	payload := bytes.Repeat([]byte("fixture "), api.ClientV2RequestLimit/8+1)
	for _, compressed := range []bool{false, true} {
		var input bytes.Buffer
		if compressed {
			writer := gzip.NewWriter(&input)
			if _, err := writer.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
		} else {
			input.Write(payload)
		}
		request := httptest.NewRequest("POST", "/api/clients/v2/rpc", &input)
		if compressed {
			request.Header.Set("Content-Encoding", "gzip")
		}
		_, err := readMaybeCompressedBody(request)
		var max *http.MaxBytesError
		if !errors.As(err, &max) {
			t.Fatalf("compressed=%t: wanted size error, got %v", compressed, err)
		}
	}
}
