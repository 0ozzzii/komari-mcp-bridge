package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	Base   *url.URL
	APIKey string
	HTTP   *http.Client
}

var nodeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func ValidNode(id string) bool { return nodeID.MatchString(id) }
func New(base, key string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u == nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || key == "" {
		return nil, errors.New("valid Komari URL and API key required")
	}
	return &Client{Base: u, APIKey: key, HTTP: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) URL(path string) string {
	u := *c.Base
	relative, _ := url.Parse(path)
	u.Path = strings.TrimRight(u.Path, "/") + relative.Path
	u.RawQuery = relative.RawQuery
	u.RawPath = ""
	return u.String()
}
func (c *Client) Request(ctx context.Context, method, path string, body io.Reader, headers http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.URL(path), body)
	if err != nil {
		return nil, err
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	return c.HTTP.Do(req)
}
func (c *Client) RPC(ctx context.Context, method string, params any) (json.RawMessage, error) {
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	resp, err := c.Request(ctx, "POST", "/api/rpc2", bytes.NewReader(b), http.Header{"Content-Type": []string{"application/json"}})
	if err != nil {
		return nil, errors.New("upstream network failure; outcome may be unknown")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("upstream HTTP status %d", resp.StatusCode)
	}
	var result struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil {
		return nil, errors.New("invalid or oversized upstream response")
	}
	if result.Error != nil {
		return nil, fmt.Errorf("upstream RPC %d: %s", result.Error.Code, result.Error.Message)
	}
	return result.Result, nil
}
func (c *Client) Terminal(ctx context.Context, node, requestID string) (*websocket.Conn, int, error) {
	return c.TerminalConfigured(ctx, node, requestID, "")
}
func (c *Client) TerminalConfigured(ctx context.Context, node, requestID, policy string) (*websocket.Conn, int, error) {
	if !ValidNode(node) {
		return nil, 0, errors.New("invalid node UUID")
	}
	u, _ := url.Parse(c.URL("/api/admin/client/" + node + "/terminal"))
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	if requestID != "" {
		q := u.Query()
		q.Set("request_id", requestID)
		u.RawQuery = q.Encode()
	}
	if policy != "" {
		q := u.Query()
		q.Set("mcp_policy", policy)
		u.RawQuery = q.Encode()
	}
	d := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 10 * time.Second}
	conn, resp, err := d.DialContext(ctx, u.String(), http.Header{"Authorization": []string{"Bearer " + c.APIKey}})
	status := 0
	if resp != nil {
		status = resp.StatusCode
		if err != nil && resp.Body != nil {
			resp.Body.Close()
		}
	}
	return conn, status, err
}
func (c *Client) ReadFile(ctx context.Context, node, path string, offset int64, max int) ([]byte, bool, error) {
	if !ValidNode(node) || offset < 0 || max < 1 || max > 131072 {
		return nil, false, errors.New("invalid file read bounds")
	}
	q := url.Values{"path": []string{path}}
	headers := http.Header{"Range": []string{fmt.Sprintf("bytes=%d-%d", offset, offset+int64(max)-1)}}
	resp, err := c.Request(ctx, "GET", "/api/admin/client/"+node+"/file/download?"+q.Encode(), nil, headers)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && !(offset == 0 && resp.StatusCode == http.StatusOK) {
		return nil, false, fmt.Errorf("file download HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, int64(max)+1))
	if len(b) > max {
		b = b[:max]
	}
	return b, len(b) == max, err
}

func (c *Client) WriteFile(ctx context.Context, node, path string, data []byte) (json.RawMessage, error) {
	if !ValidNode(node) || path == "" || len(data) > 131072 {
		return nil, errors.New("invalid bounded file write")
	}
	endpoint := "/api/admin/client/" + node + "/file/upload"
	post := func(query string, body io.Reader, contentType string) (json.RawMessage, error) {
		resp, err := c.Request(ctx, "POST", endpoint+"?"+query, body, http.Header{"Content-Type": []string{contentType}})
		if err != nil {
			return nil, errors.New("upload network error; outcome may be unknown")
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("upload HTTP %d", resp.StatusCode)
		}
		var result struct {
			Status string          `json:"status"`
			Data   json.RawMessage `json:"data"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&result) != nil || result.Status != "success" {
			return nil, errors.New("invalid upload acknowledgement")
		}
		return result.Data, nil
	}
	initial, _ := json.Marshal(map[string]any{"path": path, "size": len(data), "chunk_size": max(1, len(data))})
	raw, err := post("operation=init", bytes.NewReader(initial), "application/json")
	if err != nil || len(data) == 0 {
		return raw, err
	}
	var opened struct {
		ID string `json:"upload_id"`
	}
	if json.Unmarshal(raw, &opened) != nil || opened.ID == "" {
		return nil, errors.New("missing upload id")
	}
	q := url.Values{"operation": []string{"chunk"}, "upload_id": []string{opened.ID}, "chunk_index": []string{"0"}}
	if _, err = post(q.Encode(), bytes.NewReader(data), "application/octet-stream"); err != nil {
		return nil, err
	}
	merged, _ := json.Marshal(map[string]string{"upload_id": opened.ID})
	return post("operation=merge", bytes.NewReader(merged), "application/json")
}
