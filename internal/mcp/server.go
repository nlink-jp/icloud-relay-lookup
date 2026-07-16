// Package mcp implements a minimal Model Context Protocol server over stdio.
// It speaks newline-delimited JSON-RPC 2.0 (the MCP stdio transport) using
// only the standard library, exposing icloud-relay-lookup's cached egress
// list as MCP tools.
package mcp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/nlink-jp/icloud-relay-lookup/internal/engine"
	"github.com/nlink-jp/icloud-relay-lookup/internal/relaylist"
)

// defaultProtocolVersion is advertised when the client sends none.
const defaultProtocolVersion = "2025-06-18"

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"` // absent/null ⇒ notification
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// toolResult is the tools/call payload.
type toolResult struct {
	Content []contentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func textResult(isErr bool, text string) toolResult {
	return toolResult{Content: []contentItem{{Type: "text", Text: text}}, IsError: isErr}
}

// server holds the engine and a modtime-keyed list cache so repeated tool
// calls do not reparse the ~290k-row store, while still picking up
// update_list changes.
type server struct {
	e       *engine.Engine
	version string
	list    *relaylist.List
	listMod time.Time
}

// Serve runs the MCP protocol loop until in reaches EOF. It is safe to point
// in at os.Stdin and out at os.Stdout; diagnostics must go to stderr only.
func Serve(ctx context.Context, e *engine.Engine, version string, in io.Reader, out io.Writer) error {
	s := &server{e: e, version: version}
	dec := json.NewDecoder(in)
	enc := json.NewEncoder(out)
	for {
		var req request
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			// The stream position is unrecoverable after malformed JSON; report
			// a parse error and stop rather than spin on the same bytes.
			_ = enc.Encode(response{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &rpcError{Code: -32700, Message: "parse error: " + err.Error()},
			})
			return nil
		}
		resp, skip := s.handle(ctx, &req)
		if skip {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
}

func (s *server) handle(ctx context.Context, req *request) (response, bool) {
	// A missing or null id marks a JSON-RPC notification: never reply.
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return response{}, true
	}
	switch req.Method {
	case "initialize":
		return s.ok(req.ID, s.initializeResult(req.Params)), false
	case "ping":
		return s.ok(req.ID, struct{}{}), false
	case "tools/list":
		return s.ok(req.ID, s.toolsList()), false
	case "tools/call":
		res, rerr := s.toolsCall(ctx, req.Params)
		if rerr != nil {
			return response{JSONRPC: "2.0", ID: req.ID, Error: rerr}, false
		}
		return s.ok(req.ID, res), false
	default:
		return response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method}}, false
	}
}

func (s *server) ok(id json.RawMessage, result any) response {
	return response{JSONRPC: "2.0", ID: id, Result: result}
}

func (s *server) initializeResult(params json.RawMessage) any {
	pv := defaultProtocolVersion
	if len(params) > 0 {
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(params, &p) == nil && p.ProtocolVersion != "" {
			pv = p.ProtocolVersion
		}
	}
	return map[string]any{
		"protocolVersion": pv,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "icloud-relay-lookup", "version": s.version},
		"instructions":    Instructions,
	}
}

// load returns the cached egress list, reloading it when the store changed on
// disk (e.g. after update_list).
func (s *server) load() (*relaylist.List, error) {
	fi, err := os.Stat(s.e.Cfg.MetaPath())
	if err != nil {
		// Fall through to LoadList so the caller gets the wrapped ErrNoList message.
		return s.e.LoadList()
	}
	if s.list != nil && fi.ModTime().Equal(s.listMod) {
		return s.list, nil
	}
	list, err := s.e.LoadList()
	if err != nil {
		return nil, err
	}
	s.list, s.listMod = list, fi.ModTime()
	return list, nil
}
