package mcp

import (
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
