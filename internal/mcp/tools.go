package mcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/nlink-jp/icloud-relay-lookup/internal/engine"
)

// usageMarkdown is the operating manual returned by the get_usage tool. Its
// coherence with the real tools/results is pinned by usage_test.go.
//
//go:embed usage.md
var usageMarkdown string

// Instructions is the initialize-time hint (surfaced via the MCP
// `instructions` field) that makes get_usage discoverable and steers clients
// away from common errors.
const Instructions = "icloud-relay-lookup reports whether an IP address is an Apple iCloud Private Relay egress IP, " +
	"fully offline from a locally cached copy of Apple's published list. " +
	"Call cache_status first; if there is no list, call update_list (no credentials needed). " +
	"An IP is a Private Relay egress when is_private_relay is true; country/region/city are the geo hints Apple " +
	"publishes for that egress range. Call get_usage for the full tool reference and error-recovery table."

// toolsList returns the advertised tool set with JSON Schema for each input.
func (s *server) toolsList() any {
	strArray := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return map[string]any{
		"tools": []map[string]any{
			{
				"name":        "get_usage",
				"description": "Return this server's operating manual (markdown): the tools, the offline list lifecycle, and the error-recovery table. Call it once before first use.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			},
			{
				"name":        "check_ip",
				"description": "Report whether one or more IP addresses are Apple iCloud Private Relay egress IPs, answered offline from the cached list. Returns is_private_relay per address, plus the matched prefix and geo hints (country / region / city) on a hit.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"ip":  map[string]any{"type": "string", "description": "A single IPv4 or IPv6 address."},
						"ips": strArray,
					},
				},
			},
			{
				"name":        "update_list",
				"description": "Revalidate/download Apple's egress IP ranges list and rebuild the local store. Uses an ETag conditional GET, so an unchanged list is nearly free. No credentials required.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			},
			{
				"name":        "cache_status",
				"description": "Report the cached list's fetch time, range counts (v4/v6), ETag, source, and whether it is stale.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
	}
}

func (s *server) toolsCall(ctx context.Context, params json.RawMessage) (toolResult, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return toolResult{}, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
	}
	switch p.Name {
	case "get_usage":
		return textResult(false, usageMarkdown), nil
	case "check_ip":
		return s.toolCheckIP(p.Arguments), nil
	case "update_list":
		return s.toolUpdate(ctx), nil
	case "cache_status":
		return s.toolStatus(), nil
	default:
		return toolResult{}, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
	}
}

// checkEntry is the per-address result of check_ip.
type checkEntry struct {
	Input          string `json:"input"`
	IsPrivateRelay bool   `json:"is_private_relay"`
	Error          string `json:"error,omitempty"`
	Prefix         string `json:"prefix,omitempty"`
	Country        string `json:"country,omitempty"`
	Region         string `json:"region,omitempty"`
	City           string `json:"city,omitempty"`
}

func (s *server) toolCheckIP(args json.RawMessage) toolResult {
	var a struct {
		IP  string   `json:"ip"`
		IPs []string `json:"ips"`
	}
	_ = json.Unmarshal(args, &a)
	inputs := a.IPs
	if a.IP != "" {
		inputs = append([]string{a.IP}, inputs...)
	}
	if len(inputs) == 0 {
		return textResult(true, "provide 'ip' (string) or 'ips' (array of strings)")
	}
	list, err := s.load()
	if err != nil {
		return listErrorResult(err)
	}
	entries := make([]checkEntry, 0, len(inputs))
	for _, in := range inputs {
		r, lerr := engine.Lookup(list, in)
		if lerr != nil {
			entries = append(entries, checkEntry{Input: in, Error: "invalid address"})
			continue
		}
		ce := checkEntry{Input: in, IsPrivateRelay: r.IsRelay}
		if r.IsRelay {
			ce.Prefix = r.Entry.Prefix.String()
			ce.Country = r.Entry.Country
			ce.Region = r.Entry.Region
			ce.City = r.Entry.City
		}
		entries = append(entries, ce)
	}
	return jsonResult(entries)
}

func (s *server) toolUpdate(ctx context.Context) toolResult {
	res, err := s.e.Update(ctx)
	if err != nil {
		return textResult(true, "update failed: "+err.Error())
	}
	out := map[string]any{
		"updated":      true,
		"not_modified": res.NotModified,
		"fetched":      res.Fetched,
		"ranges":       res.Count,
		"v4":           res.V4Count,
		"v6":           res.V6Count,
		"skipped":      res.Skipped,
		"path":         s.e.Cfg.CSVPath(),
	}
	if res.ETag != "" {
		out["etag"] = res.ETag
	}
	return jsonResult(out)
}

func (s *server) toolStatus() toolResult {
	list, err := s.load()
	if err != nil {
		return listErrorResult(err)
	}
	v4, v6 := list.FamilyCounts()
	stale, age := s.e.IsStale(list.Fetched())
	out := map[string]any{
		"fetched":   list.Fetched(),
		"ranges":    list.Len(),
		"v4":        v4,
		"v6":        v6,
		"stale":     stale,
		"age_hours": int(age.Hours()),
		"source":    list.Source(),
		"path":      s.e.Cfg.CSVPath(),
	}
	if list.ETag() != "" {
		out["etag"] = list.ETag()
	}
	return jsonResult(out)
}

// jsonResult marshals v into a non-error text result.
func jsonResult(v any) toolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return textResult(true, "encode result: "+err.Error())
	}
	return textResult(false, string(b))
}

// listErrorResult renders a list load error, adding an update hint when no
// list exists yet.
func listErrorResult(err error) toolResult {
	msg := err.Error()
	if errors.Is(err, engine.ErrNoList) {
		msg += "\nCall the update_list tool to download Apple's egress IP list."
	}
	return textResult(true, msg)
}
