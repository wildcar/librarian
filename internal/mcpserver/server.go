// Package mcpserver is the service's front door (ADR-0004): one MCP endpoint;
// the Bearer token selects the account, and every tool runs inside that
// account's catalog only. Tools return the de-identified summary by default
// (ADR-0005); source paths never leave the service.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
)

// Instructions tell the assistant how to use the archive.
const Instructions = `Архив документов одного аккаунта: документы нескольких людей (list_persons).
search_documents ищет по словам и фильтрует по человеку и датам; без слов перечисляет все документы,
новые первыми. total — сколько документов подходит всего; пока есть next_offset, повтори запрос
с offset=next_offset, чтобы просмотреть все. get_document отдаёт документ.
У документа может не быть даты или человека: так бывает, документ от этого не хуже, по нему просто
меньше известно. missing=["date"] без фильтра по дате находит документы без даты, а вместе с
date_from/date_to — документы в периоде или без даты; так же missing=["person"] и person.
Если в документе нет однозначного ответа, так и скажи, не додумывай.
По умолчанию отдаются только резюме без персональных данных. Суть документа (layer=essential) и
дословный текст (layer=verbatim) запрашивай, только если резюме не хватает для ответа: в них есть
имена, номера и названия организаций. review перечисляет, что владелец ещё не проверил
(распознавание страницы, границы документа, резюме); пустой список — замечаний нет.`

// Server serves the MCP endpoint for every account under one data root.
type Server struct {
	home string

	mu       sync.Mutex
	catalogs map[string]*catalog.Catalog
	servers  map[string]*mcp.Server
}

// New prepares a server over the data root.
func New(home string) *Server {
	return &Server{home: home, catalogs: map[string]*catalog.Catalog{}, servers: map[string]*mcp.Server{}}
}

// Close closes the account catalogs it opened.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var errs []error
	for _, c := range s.catalogs {
		errs = append(errs, c.Close())
	}
	return errors.Join(errs...)
}

type ctxKey struct{}

// Handler is the HTTP handler: /health is open, everything else needs a token.
func (s *Server) Handler() http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		a, _ := r.Context().Value(ctxKey{}).(*account.Account)
		if a == nil {
			return nil // unreachable: the middleware rejects such requests
		}
		srv, err := s.forAccount(a)
		if err != nil {
			return nil
		}
		return srv
	}, &mcp.StreamableHTTPOptions{Stateless: true})

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		a, err := account.Authenticate(s.home, strings.TrimSpace(tok))
		if !ok || err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="librarian"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, a)))
	}))
	return mux
}

// forAccount returns the MCP server bound to one account, built once.
func (s *Server) forAccount(a *account.Account) (*mcp.Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if srv, ok := s.servers[a.ID]; ok {
		return srv, nil
	}
	c, err := catalog.Open(a.CatalogPath())
	if err != nil {
		return nil, err
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "librarian", Version: "0"},
		&mcp.ServerOptions{Instructions: Instructions})
	t := &tools{cat: c}
	mcp.AddTool(srv, &mcp.Tool{Name: "list_persons",
		Description: "Люди, чьи документы хранит аккаунт: код, метка и число документов. Полных имён нет."}, t.listPersons)
	mcp.AddTool(srv, &mcp.Tool{Name: "search_documents",
		Description: "Поиск и перечисление документов по словам, человеку и датам, с отсутствующими характеристиками и постранично. Возвращает резюме без персональных данных."}, t.search)
	mcp.AddTool(srv, &mcp.Tool{Name: "get_document",
		Description: "Документ по id: резюме (по умолчанию), суть (essential) или дословный текст (verbatim)."}, t.get)
	s.catalogs[a.ID], s.servers[a.ID] = c, srv
	return srv, nil
}

type tools struct{ cat *catalog.Catalog }

// --- list_persons ------------------------------------------------------------

type noInput struct{}

type personOut struct {
	Code      string `json:"code"`
	Label     string `json:"label"`
	Documents int    `json:"documents"`
}

type personsOut struct {
	Persons []personOut `json:"persons"`
}

func (t *tools) listPersons(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, personsOut, error) {
	ps, err := t.cat.Persons(ctx)
	if err != nil {
		return nil, personsOut{}, err
	}
	out := personsOut{Persons: []personOut{}}
	for _, p := range ps {
		out.Persons = append(out.Persons, personOut{Code: p.Code, Label: p.Label, Documents: p.Documents})
	}
	return nil, out, nil
}

// --- search_documents --------------------------------------------------------

type searchIn struct {
	Query    string   `json:"query,omitempty" jsonschema:"слова для поиска, например «кардиолог эхокардиография»"`
	Person   string   `json:"person,omitempty" jsonschema:"код человека из list_persons"`
	DateFrom string   `json:"date_from,omitempty" jsonschema:"не раньше этой даты документа, ГГГГ-ММ-ДД"`
	DateTo   string   `json:"date_to,omitempty" jsonschema:"не позже этой даты документа, ГГГГ-ММ-ДД"`
	Missing  []string `json:"missing,omitempty" jsonschema:"характеристики, которых может не быть: date, person. Без фильтра по ней — только документы без неё; с фильтром — подходящие по фильтру или без неё"`
	Offset   int      `json:"offset,omitempty" jsonschema:"сколько первых документов пропустить: next_offset из прошлого ответа"`
	Limit    int      `json:"limit,omitempty" jsonschema:"сколько документов вернуть, до 50 (по умолчанию 20)"`
}

type docOut struct {
	ID      string   `json:"id"`
	Date    string   `json:"date,omitempty"`
	Persons []string `json:"persons"`
	Pages   int      `json:"pages"`
	Summary string   `json:"summary"`
	Review  []string `json:"review"`
}

type searchOut struct {
	Total      int      `json:"total"`
	NextOffset int      `json:"next_offset,omitempty"`
	Documents  []docOut `json:"documents"`
}

var isoDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func (t *tools) search(ctx context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, searchOut, error) {
	for _, d := range []string{in.DateFrom, in.DateTo} {
		if d != "" && !isoDate.MatchString(d) {
			return nil, searchOut{}, fmt.Errorf("дата %q: нужен формат ГГГГ-ММ-ДД", d)
		}
	}
	for _, m := range in.Missing {
		if m != catalog.MissingDate && m != catalog.MissingPerson {
			return nil, searchOut{}, fmt.Errorf("missing: %q — допустимы date и person", m)
		}
	}
	if in.Offset < 0 {
		return nil, searchOut{}, fmt.Errorf("offset не может быть отрицательным")
	}
	hits, total, err := t.cat.Search(ctx, catalog.SearchQuery{
		Text: in.Query, Person: in.Person, DateFrom: in.DateFrom, DateTo: in.DateTo,
		Missing: in.Missing, Offset: in.Offset, Limit: in.Limit,
	})
	if err != nil {
		return nil, searchOut{}, err
	}
	out := searchOut{Total: total, Documents: []docOut{}}
	for _, h := range hits {
		out.Documents = append(out.Documents, docOut{ID: h.DocID, Date: h.Date, Persons: nonNil(h.Persons),
			Pages: h.Pages, Summary: h.Summary, Review: nonNil(h.Review)})
	}
	if next := in.Offset + len(hits); len(hits) > 0 && next < total {
		out.NextOffset = next
	}
	return nil, out, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// --- get_document ------------------------------------------------------------

type getIn struct {
	ID    string `json:"id" jsonschema:"id документа из search_documents"`
	Layer string `json:"layer,omitempty" jsonschema:"summary (по умолчанию), essential или verbatim"`
}

type getOut struct {
	ID         string   `json:"id"`
	Date       string   `json:"date,omitempty"`
	DateSource string   `json:"date_source,omitempty"`
	Persons    []string `json:"persons"`
	Pages      int      `json:"pages"`
	Layer      string   `json:"layer"`
	Text       string   `json:"text"`
	Review     []string `json:"review"`
}

func (t *tools) get(ctx context.Context, _ *mcp.CallToolRequest, in getIn) (*mcp.CallToolResult, getOut, error) {
	layer := in.Layer
	if layer == "" {
		layer = "summary"
	}
	if layer != "summary" && layer != "essential" && layer != "verbatim" {
		return nil, getOut{}, fmt.Errorf("layer должен быть summary, essential или verbatim")
	}
	d, tx, err := t.cat.GetDocument(ctx, in.ID)
	if err != nil {
		return nil, getOut{}, fmt.Errorf("документ %q не найден", in.ID)
	}
	persons, err := t.cat.DocPersons(ctx, d.ID)
	if err != nil {
		return nil, getOut{}, err
	}
	date, source, err := t.cat.DocDate(ctx, d.ID)
	if err != nil {
		return nil, getOut{}, err
	}
	review, err := t.cat.DocReview(ctx, d.ID)
	if err != nil {
		return nil, getOut{}, err
	}
	out := getOut{ID: d.ID, Date: date, DateSource: source, Persons: nonNil(persons),
		Pages: d.LastPage - d.FirstPage + 1, Layer: layer, Review: nonNil(review)}
	switch layer {
	case "summary":
		out.Text = tx.Summary
	case "essential":
		out.Text = tx.Essential
	case "verbatim":
		ps, err := t.cat.Pages(ctx, d.SHA256)
		if err != nil {
			return nil, getOut{}, err
		}
		var b strings.Builder
		for _, p := range ps {
			if p.Page >= d.FirstPage && p.Page <= d.LastPage {
				fmt.Fprintf(&b, "--- страница %d ---\n%s\n", p.Page-d.FirstPage+1, p.Text)
			}
		}
		out.Text = b.String()
	}
	if tx.Status == "" && layer != "verbatim" {
		out.Text = "Резюме и суть ещё не готовы."
	}
	return nil, out, nil
}
