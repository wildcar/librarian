// Package router is the MCP client for model-router-mcp. Every model call of
// the service goes through it (AGENTS.md project rules).
package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ChatRequest is one `chat` call.
type ChatRequest struct {
	Account   string   // becomes external_user_id "librarian:<account>" (ADR-0004)
	Model     string   // pinned model_id
	Text      string   // the prompt
	ImagesB64 []string // optional page images
	ImageMIME string
}

// ChatReply is the useful part of a `chat` result.
type ChatReply struct {
	Text    string
	Model   string  // the model that actually answered
	CostUSD float64 // as computed by the router
}

// Chatter is what the pipeline needs from the router; tests use a fake.
type Chatter interface {
	Chat(ctx context.Context, r ChatRequest) (ChatReply, error)
}

// Client is a connected router session.
type Client struct {
	session *mcp.ClientSession
	appURL  string
}

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

// Dial connects to the router's MCP endpoint.
func Dial(ctx context.Context, endpoint, token string) (*Client, error) {
	if endpoint == "" || token == "" {
		return nil, errors.New("router: ROUTER_URL and ROUTER_TOKEN must be set")
	}
	hc := &http.Client{Transport: bearer{token: token, next: http.DefaultTransport}}
	c := mcp.NewClient(&mcp.Implementation{Name: "librarian", Version: "0"}, nil)
	s, err := c.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: hc, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("router: connect %s: %w", endpoint, err)
	}
	return &Client{session: s}, nil
}

// SetApp names the site the requests come from (app_url, e.g.
// https://lib.wildcar.ru): the router logs it and passes it to providers.
// Unset, the router records the requests as its own.
func (c *Client) SetApp(url string) { c.appURL = url }

// Close ends the session.
func (c *Client) Close() error { return c.session.Close() }

// Chat calls the router's `chat` tool.
func (c *Client) Chat(ctx context.Context, r ChatRequest) (ChatReply, error) {
	args := map[string]any{
		"external_user_id": "librarian:" + r.Account,
		"app_name":         "librarian",
		"model_id":         r.Model,
		"text":             r.Text,
		"params":           map[string]any{"temperature": 0},
	}
	if c.appURL != "" {
		args["app_url"] = c.appURL
	}
	if len(r.ImagesB64) > 0 {
		args["images_b64"] = r.ImagesB64
		args["image_mime"] = r.ImageMIME
	}
	res, err := c.session.CallTool(ctx, &mcp.CallToolParams{Name: "chat", Arguments: args})
	if err != nil {
		return ChatReply{}, fmt.Errorf("router chat: %w", err)
	}
	if res.IsError {
		return ChatReply{}, fmt.Errorf("router chat: %s", textOf(res))
	}
	return parseReply(res)
}

func textOf(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func parseReply(res *mcp.CallToolResult) (ChatReply, error) {
	var raw []byte
	if res.StructuredContent != nil {
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			return ChatReply{}, fmt.Errorf("router chat: %w", err)
		}
		raw = b
	} else {
		raw = []byte(textOf(res))
	}
	var v struct {
		Result *struct {
			Text    string  `json:"text"`
			ModelID string  `json:"model_id"`
			CostUSD float64 `json:"cost_usd"`
		} `json:"result"`
		Text    string  `json:"text"`
		ModelID string  `json:"model_id"`
		CostUSD float64 `json:"cost_usd"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ChatReply{}, fmt.Errorf("router chat: unexpected reply: %w", err)
	}
	if v.Result != nil { // FastMCP wraps non-object returns as {"result": ...}
		return ChatReply{Text: v.Result.Text, Model: v.Result.ModelID, CostUSD: v.Result.CostUSD}, nil
	}
	return ChatReply{Text: v.Text, Model: v.ModelID, CostUSD: v.CostUSD}, nil
}
