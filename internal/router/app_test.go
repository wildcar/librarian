package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The chat call names the site it comes from, so the router does not record
// the requests as its own.
func TestChatSendsApp(t *testing.T) {
	var got map[string]any
	srv := mcp.NewServer(&mcp.Implementation{Name: "fake-router"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "chat"}, func(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		got = in
		return &mcp.CallToolResult{StructuredContent: map[string]any{"text": "ok", "model_id": "m", "cost_usd": 0.0}}, nil, nil
	})
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))
	defer ts.Close()

	ctx := context.Background()
	c, err := Dial(ctx, ts.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetApp("https://lib.example.org")
	if _, err := c.Chat(ctx, ChatRequest{Account: "acc", Model: "m", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if got["app_url"] != "https://lib.example.org" || got["app_name"] != "librarian" || got["external_user_id"] != "librarian:acc" {
		t.Errorf("arguments: %v", got)
	}
}
