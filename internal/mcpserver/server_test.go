package mcpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/index"
	"github.com/wildcar/librarian/internal/mcpserver"
)

// seed creates an account with one person and one document whose layers are
// ready, and returns the account token and the document id.
func seed(t *testing.T, home, id, name, summary, essential string) (string, string) {
	t.Helper()
	ctx := context.Background()
	a, err := account.Create(home, id)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(a.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	now := time.Now()
	sha := strings.Repeat(id[:1], 64)
	if _, err := c.RecordSeen(ctx, catalog.Seen{URI: "file:///Семья/" + name + "/scan.pdf", SHA256: sha, Size: 1, MTime: now, MIME: "application/pdf", At: now}); err != nil {
		t.Fatal(err)
	}
	if err := c.SavePage(ctx, catalog.Page{SHA256: sha, Page: 1, Method: "ocr", Engine: "t", Text: "Пациент: " + name + "\n" + essential, Pipeline: 1}, now); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveSplit(ctx, catalog.SplitResult{SHA256: sha, Status: catalog.SplitDone, Method: "single", Engine: "-", Pipeline: 1, Starts: []int{1}, Pages: 1}, now); err != nil {
		t.Fatal(err)
	}
	doc := catalog.DocumentID(sha, 1)
	if err := c.SaveTexts(ctx, catalog.Texts{DocID: doc, Status: catalog.TextsDone, Summary: summary, Essential: "Пациент: " + name + "\n" + essential, Model: "t", Pipeline: 1}, now); err != nil {
		t.Fatal(err)
	}
	if err := c.AddPerson(ctx, catalog.Person{Code: "P" + id[:1], Label: "Папа", FullName: name}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Run(ctx, c, now); err != nil {
		t.Fatal(err)
	}
	tok, err := a.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	return tok, doc
}

type bearer struct {
	token string
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func connect(t *testing.T, url, token string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	s, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: url, HTTPClient: &http.Client{Transport: bearer{token}}, DisableStandaloneSSE: true, MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func call(t *testing.T, s *mcp.ClientSession, tool string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var out map[string]any
	b, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(b, &out)
	return out, res.IsError
}

func TestServerIsolationAndTools(t *testing.T) {
	home := t.TempDir()
	tokA, docA := seed(t, home, "alpha", "Иванов Петр Сергеевич",
		"Протокол эхокардиографии от 02.04.2022: пациент, кардиолог, норма.", "Эхокардиография: норма. Иванов.")
	tokB, docB := seed(t, home, "beta", "Петров Илья Олегович",
		"Протокол эхокардиографии от 05.05.2023: пациент, кардиолог, стеноз.", "Эхокардиография: стеноз.")

	srv := mcpserver.New(home)
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	url := ts.URL + "/mcp"

	// No token, a wrong token: refused before any MCP traffic.
	for name, tok := range map[string]string{"none": "", "wrong": "lib_alpha_nope"} {
		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{}`))
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s token: status %d", name, resp.StatusCode)
		}
	}

	a := connect(t, url, tokA)
	persons, _ := call(t, a, "list_persons", nil)
	pb, _ := json.Marshal(persons)
	if !strings.Contains(string(pb), `"Папа"`) || strings.Contains(string(pb), "Иванов") || strings.Contains(string(pb), "Петров") {
		t.Errorf("list_persons: %s", pb)
	}

	// Both accounts hold an echo protocol; alpha sees only its own.
	found, _ := call(t, a, "search_documents", map[string]any{"query": "эхокардиографию"})
	fb, _ := json.Marshal(found)
	if !strings.Contains(string(fb), docA) || strings.Contains(string(fb), docB) {
		t.Errorf("alpha search: %s", fb)
	}
	if strings.Contains(string(fb), "Семья") || strings.Contains(string(fb), "scan.pdf") {
		t.Errorf("search leaks source paths: %s", fb)
	}
	byPerson, _ := call(t, a, "search_documents", map[string]any{"person": "Pa", "date_from": "2022-01-01", "date_to": "2022-12-31"})
	if docs, _ := byPerson["documents"].([]any); len(docs) != 1 {
		t.Errorf("person and date filter: %v", byPerson)
	}
	if _, isErr := call(t, a, "search_documents", map[string]any{"date_from": "02.04.2022"}); !isErr {
		t.Error("bad date format accepted")
	}

	// Summary by default; essential on request; another account's id is not found.
	doc, _ := call(t, a, "get_document", map[string]any{"id": docA})
	if doc["layer"] != "summary" || strings.Contains(doc["text"].(string), "Иванов") || doc["date"] != "2022-04-02" {
		t.Errorf("get_document default: %v", doc)
	}
	ess, _ := call(t, a, "get_document", map[string]any{"id": docA, "layer": "essential"})
	if !strings.Contains(ess["text"].(string), "Иванов") {
		t.Errorf("essential on request: %v", ess)
	}
	if _, isErr := call(t, a, "get_document", map[string]any{"id": docB}); !isErr {
		t.Error("alpha read beta's document")
	}

	b := connect(t, url, tokB)
	other, _ := call(t, b, "search_documents", map[string]any{"query": "стеноз"})
	ob, _ := json.Marshal(other)
	if !strings.Contains(string(ob), docB) || strings.Contains(string(ob), docA) {
		t.Errorf("beta search: %s", ob)
	}
}
