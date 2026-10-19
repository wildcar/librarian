package router

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestParseReply(t *testing.T) {
	flat := &mcp.CallToolResult{StructuredContent: map[string]any{
		"text": "страница", "model_id": "google/gemini-4-argon", "cost_usd": 0.0097,
	}}
	wrapped := &mcp.CallToolResult{StructuredContent: map[string]any{
		"result": map[string]any{"text": "страница", "model_id": "google/gemini-4-argon", "cost_usd": 0.0097},
	}}
	textOnly := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
		Text: `{"text": "страница", "model_id": "google/gemini-4-argon", "cost_usd": 0.0097}`,
	}}}
	for name, res := range map[string]*mcp.CallToolResult{"flat": flat, "wrapped": wrapped, "text": textOnly} {
		r, err := parseReply(res)
		if err != nil || r.Text != "страница" || r.Model != "google/gemini-4-argon" || r.CostUSD != 0.0097 {
			t.Errorf("%s: %+v, %v", name, r, err)
		}
	}
}
