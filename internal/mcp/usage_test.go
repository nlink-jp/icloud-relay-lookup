package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestUsageDocumentsEveryTool keeps usage.md coherent with the advertised tool
// set: a renamed or added tool that the manual does not mention fails here.
func TestUsageDocumentsEveryTool(t *testing.T) {
	s := &server{}
	list := s.toolsList().(map[string]any)
	tools, ok := list["tools"].([]map[string]any)
	if !ok || len(tools) == 0 {
		t.Fatal("toolsList returned no tools")
	}
	for _, tl := range tools {
		name, _ := tl["name"].(string)
		if !strings.Contains(usageMarkdown, name) {
			t.Errorf("usage.md does not document tool %q", name)
		}
	}
	// Result fields and knobs that agents depend on must stay documented.
	for _, term := range []string{
		"is_private_relay", "not_modified", "ranges", "update_list",
		"egress-ip-ranges.csv", "country", "region", "city", "prefix",
	} {
		if !strings.Contains(usageMarkdown, term) {
			t.Errorf("usage.md missing key term %q", term)
		}
	}
}

// TestEveryToolSchemaIsValidAndClosed keeps a mistyped argument from reading as
// a real one: org ADR-021 §10 requires every registered schema to set
// additionalProperties:false, and requires this assertion to exist, because a
// rule stated only in prose is re-decided by whoever adds the next tool.
//
// The flag is the declared half of the contract — what a schema-checking client
// refuses before the call. This server still decodes arguments with a plain
// json.Unmarshal, so an unknown argument that arrives anyway is ignored rather
// than refused; see AGENTS.md.
func TestEveryToolSchemaIsValidAndClosed(t *testing.T) {
	s := &server{}
	b, err := json.Marshal(s.toolsList())
	if err != nil {
		t.Fatalf("marshal tool list: %v", err)
	}
	var list struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Type                 string `json:"type"`
				AdditionalProperties *bool  `json:"additionalProperties"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatalf("tool list is not valid JSON: %v", err)
	}
	// Without this the loop below passes by having nothing to check.
	if len(list.Tools) == 0 {
		t.Fatal("toolsList returned no tools")
	}
	for _, tool := range list.Tools {
		if tool.InputSchema.Type != "object" {
			t.Errorf("%s: schema type = %q, want object", tool.Name, tool.InputSchema.Type)
		}
		if tool.InputSchema.AdditionalProperties == nil || *tool.InputSchema.AdditionalProperties {
			t.Errorf("%s: schema should set additionalProperties:false so typos are caught", tool.Name)
		}
	}
}
