// Package mcp serves icloud-relay-lookup's cached egress list as Model Context
// Protocol tools over stdio. The protocol itself — both eras (the legacy
// initialize handshake and the 2026-07-28 revision), framing, versions,
// cancellation — is the shared module nlink-jp/mcp's (organization ADR-030,
// ADR-0001); this package holds the tools.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/nlink-jp/mcp"
	mcpserver "github.com/nlink-jp/mcp/server"

	"github.com/nlink-jp/icloud-relay-lookup/internal/engine"
	"github.com/nlink-jp/icloud-relay-lookup/internal/relaylist"
)

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

// Serve runs the MCP server until in reaches EOF (answering every request that
// arrived), ctx is cancelled, or a write to out fails. The protocol itself —
// both eras (the legacy initialize handshake and the 2026-07-28 revision),
// framing, versions, cancellation — is nlink-jp/mcp's (organization ADR-030);
// this package holds the tools. Diagnostics go to stderr only.
func Serve(ctx context.Context, e *engine.Engine, version string, in io.Reader, out io.Writer) error {
	s := &server{e: e, version: version}
	ts, err := s.toolServer()
	if err != nil {
		return err
	}
	return ts.Serve(ctx, in, out)
}

// toolServer registers the advertised tools, in their listed order, on a
// fresh protocol server. Each handler calls toolsCall, which dispatches by
// name and builds the tool's result.
func (s *server) toolServer() (*mcpserver.ToolServer, error) {
	ts, err := mcpserver.New(mcpserver.Info{
		Server:       mcp.Implementation{Name: "icloud-relay-lookup", Version: s.version},
		Instructions: Instructions,
	}, mcpserver.Options{
		// One call at a time, as before the module: state in the server and
		// its engine assumes it.
		Workers: 1,
		// Calls sent side by side wait, as they did before the module: the
		// default of 16 would refuse the 17th as "server busy".
		QueueDepth: 256,
		Logger:     slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		return nil, err
	}
	for _, def := range s.toolsList() {
		schema, err := json.Marshal(def["inputSchema"])
		if err != nil {
			return nil, fmt.Errorf("tool %v: %w", def["name"], err)
		}
		name, okName := def["name"].(string)
		desc, okDesc := def["description"].(string)
		if !okName || !okDesc {
			return nil, fmt.Errorf("tool %v: name and description must be strings", def["name"])
		}
		if err := ts.AddTool(mcpserver.Tool{Name: name, Description: desc, InputSchema: schema}, s.handle); err != nil {
			return nil, err
		}
	}
	return ts, nil
}

// handle runs one tools/call. A tool failure is a result with isError set;
// the protocol layer has already refused an unknown tool and arguments that
// are not an object.
func (s *server) handle(ctx context.Context, req *mcpserver.Request) (*mcpserver.Result, error) {
	res := s.toolsCall(ctx, req.Name, req.Arguments)
	out := &mcpserver.Result{IsError: res.IsError}
	for _, c := range res.Content {
		b, err := json.Marshal(c)
		if err != nil {
			return nil, err
		}
		out.Content = append(out.Content, b)
	}
	return out, nil
}
