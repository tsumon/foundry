package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"foundry/internal/domain"
)

type Result struct {
	ProtocolVersion string
	ServerName      string
	ServerVersion   string
	Tools           []domain.MCPTool
	Digest          string
	ObservedAt      string
	Requests        int
	ResponseBytes   int64
}

// Error deliberately never includes endpoint, remote text, or credentials.
type Error struct {
	Code                string
	Unknown             bool
	CallMayHaveBeenSent bool
}

func (e *Error) Error() string                { return e.Code }
func failure(code string, unknown bool) error { return &Error{Code: code, Unknown: unknown} }

type discovery struct {
	ctx                           context.Context
	client                        *http.Client
	endpoint, credential, session string
	result                        Result
	notifications                 int
	maxRequests                   int
	requestBytes                  int64
	callStarted                   bool
	callResponseReceived          bool
}

func Discover(ctx context.Context, endpoint, credential string) (result Result, err error) {
	if len(credential) > 8192 || strings.ContainsFunc(credential, unicode.IsControl) {
		return result, failure("protocol_error", false)
	}
	client, transport, err := newHTTPClient(endpoint)
	if err != nil {
		return result, err
	}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	d := &discovery{ctx: ctx, client: client, endpoint: endpoint, credential: credential, maxRequests: 13, result: Result{Tools: []domain.MCPTool{}}}
	defer func() { d.cleanup(); result = d.result }()
	if err = d.initialize(); err != nil {
		return result, err
	}
	if err = d.listTools(); err != nil {
		return result, err
	}
	return result, nil
}

func (d *discovery) initialize() error {
	raw, err := d.post(1, "initialize", map[string]any{"protocolVersion": domain.MCPProtocol, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "FOUNDRY", "version": "0.1.0"}})
	if err != nil {
		return err
	}
	var init struct {
		Protocol     string                     `json:"protocolVersion"`
		Capabilities map[string]json.RawMessage `json:"capabilities"`
		Server       struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if json.Unmarshal(raw, &init) != nil {
		return failure("invalid_response", false)
	}
	if init.Protocol != domain.MCPProtocol {
		return failure("unsupported_protocol", false)
	}
	if !object(init.Capabilities["tools"]) || !validText(init.Server.Name, 1024, true) || !validText(init.Server.Version, 1024, true) {
		return failure("protocol_error", false)
	}
	d.result.ProtocolVersion = init.Protocol
	d.result.ServerName = init.Server.Name
	d.result.ServerVersion = init.Server.Version
	if _, err = d.post(0, "notifications/initialized", nil); err != nil {
		return err
	}
	return nil
}

func (d *discovery) listTools() error {
	names, cursors := map[string]bool{}, map[string]bool{}
	cursor := ""
	for page := 0; page < 10; page++ {
		params := map[string]string{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := d.post(page+2, "tools/list", params)
		if err != nil {
			return err
		}
		var listing struct {
			Tools      []json.RawMessage `json:"tools"`
			NextCursor json.RawMessage   `json:"nextCursor"`
		}
		if json.Unmarshal(raw, &listing) != nil || listing.Tools == nil {
			return failure("invalid_response", false)
		}
		if len(d.result.Tools)+len(listing.Tools) > 500 {
			return failure("budget_exceeded", false)
		}
		for _, definition := range listing.Tools {
			tool, e := parseTool(definition)
			if e != nil {
				return e
			}
			if names[tool.Name] {
				return failure("protocol_error", false)
			}
			names[tool.Name] = true
			d.result.Tools = append(d.result.Tools, tool)
		}
		cursor = ""
		if len(listing.NextCursor) > 0 && (json.Unmarshal(listing.NextCursor, &cursor) != nil || cursor == "") {
			return failure("invalid_response", false)
		}
		if len(cursor) > 4096 {
			return failure("budget_exceeded", false)
		}
		if cursor == "" {
			d.result.Digest, err = toolSnapshotDigest(d.result.Tools)
			if err != nil {
				return failure("invalid_response", false)
			}
			d.result.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
			return nil
		}
		if cursors[cursor] {
			return failure("protocol_error", false)
		}
		cursors[cursor] = true
	}
	return failure("budget_exceeded", false)
}

func (d *discovery) post(id int, method string, params any) (json.RawMessage, error) {
	return d.postWithin(id, method, params, 5*time.Second)
}

func (d *discovery) postWithin(id int, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	request := map[string]any{"jsonrpc": "2.0", "method": method}
	if id > 0 {
		request["id"] = id
	}
	if params != nil {
		request["params"] = params
	}
	raw, _ := json.Marshal(request)
	ctx, cancel := context.WithTimeout(d.ctx, timeout)
	defer cancel()
	response, err := d.send(ctx, "POST", raw)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if d.callStarted {
		d.callResponseReceived = true
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, failure("protocol_error", false)
	}
	sessionValues := response.Header.Values("Mcp-Session-Id")
	if len(sessionValues) > 1 {
		return nil, failure("protocol_error", false)
	}
	responseSession := response.Header.Get("Mcp-Session-Id")
	if id == 1 && responseSession != "" {
		session := responseSession
		if len(session) > 1024 || strings.IndexFunc(session, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0 {
			return nil, failure("protocol_error", false)
		}
		d.session = session
	} else if id != 1 && responseSession != "" && responseSession != d.session {
		return nil, failure("protocol_error", false)
	}
	body := &countedBody{reader: response.Body, d: d}
	if id == 0 {
		data, e := io.ReadAll(body)
		if e != nil {
			return nil, bodyError(e)
		}
		if response.StatusCode != 202 || len(data) != 0 {
			return nil, failure("protocol_error", false)
		}
		return nil, nil
	}
	media, _, e := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if e != nil {
		return nil, failure("invalid_response", false)
	}
	if media == "text/event-stream" {
		return d.readSSE(body, id)
	}
	if media != "application/json" {
		return nil, failure("invalid_response", false)
	}
	data, e := io.ReadAll(body)
	if e != nil {
		return nil, bodyError(e)
	}
	result, notification, e := d.message(data, id)
	if notification && e == nil {
		e = failure("invalid_response", false)
	}
	return result, e
}

func (d *discovery) send(ctx context.Context, method string, body []byte) (*http.Response, error) {
	maxRequests := d.maxRequests
	if maxRequests == 0 {
		maxRequests = 13
	}
	if d.result.Requests >= maxRequests || d.result.ResponseBytes >= 4<<20 {
		return nil, failure("budget_exceeded", false)
	}
	if err := ctx.Err(); err != nil {
		return nil, failure("canceled", d.result.Requests > 0)
	}
	request, err := http.NewRequestWithContext(ctx, method, d.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, failure("protocol_error", false)
	}
	request.Header.Set("Accept", "application/json, text/event-stream")
	if method == "POST" {
		request.Header.Set("Content-Type", "application/json")
	}
	if d.result.Requests > 0 {
		request.Header.Set("MCP-Protocol-Version", domain.MCPProtocol)
	}
	if d.session != "" {
		request.Header.Set("Mcp-Session-Id", d.session)
	}
	if d.credential != "" {
		request.Header.Set("Authorization", "Bearer "+d.credential)
	}
	d.result.Requests++
	d.requestBytes += int64(len(body))
	response, err := d.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, failure("canceled", true)
		}
		return nil, failure("transport_error", true)
	}
	// A response can win the transport's race with cancellation. Do not parse
	// that interrupted exchange as a definite rejection, and retain body ownership.
	if ctx.Err() != nil {
		response.Body.Close()
		return nil, failure("canceled", true)
	}
	return response, nil
}

func (d *discovery) cleanup() {
	maxRequests := d.maxRequests
	if maxRequests == 0 {
		maxRequests = 13
	}
	if d.session == "" || d.ctx.Err() != nil || d.result.Requests >= maxRequests || d.result.ResponseBytes >= 4<<20 {
		return
	}
	ctx, cancel := context.WithTimeout(d.ctx, time.Second)
	defer cancel()
	response, err := d.send(ctx, "DELETE", nil)
	if err == nil {
		// Cleanup cannot invalidate a complete catalog. Read only its remaining
		// byte allowance (never one-byte overshoot), then close without retry.
		remaining := int64(4<<20) - d.result.ResponseBytes
		if remaining > 1<<20 {
			remaining = 1 << 20
		}
		n, _ := io.Copy(io.Discard, io.LimitReader(response.Body, remaining))
		d.result.ResponseBytes += n
		response.Body.Close()
	}
}

type countedBody struct {
	reader io.Reader
	d      *discovery
	n      int64
}

func (b *countedBody) Read(p []byte) (int, error) {
	remaining := int64(1<<20) - b.n
	if total := int64(4<<20) - b.d.result.ResponseBytes; total < remaining {
		remaining = total
	}
	if int64(len(p)) > remaining+1 {
		p = p[:remaining+1]
	}
	n, err := b.reader.Read(p)
	b.n += int64(n)
	b.d.result.ResponseBytes += int64(n)
	if b.n > 1<<20 || b.d.result.ResponseBytes > 4<<20 {
		// Do not expose a buffered result alongside a budget error: a streaming
		// parser could otherwise return it before observing that error.
		return 0, failure("budget_exceeded", false)
	}
	return n, err
}
func bodyError(err error) error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return failure("transport_error", true)
}

func validText(s string, max int, required bool) bool {
	return utf8.ValidString(s) && len(s) <= max && !strings.ContainsRune(s, 0) && (!required || strings.TrimSpace(s) != "")
}
func object(raw []byte) bool {
	return len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '{' && json.Valid(raw)
}

func parseTool(raw json.RawMessage) (domain.MCPTool, error) {
	tool, err := parseToolDefinition(raw)
	if err != nil {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if len(raw) > 1<<20 || len(fields["inputSchema"]) > 64<<10 || len(fields["outputSchema"]) > 64<<10 {
			return domain.MCPTool{}, failure("budget_exceeded", false)
		}
		return domain.MCPTool{}, failure("invalid_response", false)
	}
	return tool, nil
}
