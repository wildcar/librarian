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
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/index"
)

// Instructions tell the assistant how to use the archive.
const Instructions = `Архив документов одного аккаунта: документы нескольких людей (list_persons).
search_documents ищет по словам и фильтрует по человеку и датам; без слов перечисляет все документы,
новые первыми. total — сколько документов подходит всего; пока есть next_offset, повтори запрос
с offset=next_offset, чтобы просмотреть все. get_document отдаёт документ.
У документа может не быть даты или человека: так бывает, документ от этого не хуже, по нему просто
меньше известно. date — дата создания документа (когда его составили, выдали, подписали), а не
дата события, на которое он ссылается; date_source в get_document говорит, откуда она. Если у даты
нет дня или года, она в date_partial (ГГГГ-ММ, ГГГГ или --ММ-ДД), а date пусто: недостающее не
угадано, фильтры по датам такой документ считают документом без даты.
attrs — характеристики документа (ключ → значения), открытый набор: у разных документов он разный.
Владелец задаёт их пачке документов, присланных вместе (batches — номера пачек). Фильтр
attrs=["ключ=значение"] требует все пары, batch — документы одной пачки. missing=["date"] без фильтра по дате находит документы без даты, а вместе с
date_from/date_to — документы в периоде или без даты; так же missing=["person"] и person, а любой
другой ключ в missing — характеристика с этим ключом (вместе с attrs по нему — значение или её нет).
Если в документе нет однозначного ответа, так и скажи, не додумывай.
title — короткое название документа для списка, summary — его резюме.
По умолчанию отдаются только резюме без персональных данных. Суть документа (layer=essential) и
дословный текст (layer=verbatim) запрашивай, только если резюме не хватает для ответа: в них есть
имена, номера и названия организаций. review перечисляет, что владелец ещё не проверил
(распознавание страницы, границы документа, резюме); пустой список — замечаний нет.
update_document меняет документ: вызывай его, только когда пользователь сам сказал, что исправить
(«это мамин документ», «дата — 12 сентября»), а не по своей догадке, и передай в reason его слова.
Правка видна как правка ассистента, пока владелец её не подтвердит; то, что владелец задал вручную,
ассистент изменить не может.`

// Server serves the MCP endpoint for every account under one data root.
type Server struct {
	home string

	mu       sync.Mutex
	catalogs map[string]*catalog.Catalog
	servers  map[string]*mcp.Server

	web         http.Handler
	oauth       OAuth
	metadataURL string
}

// SetWeb gives every path but /health and /mcp to the web interface (ADR-0007).
func (s *Server) SetWeb(h http.Handler) { s.web = h }

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

// OAuth checks a token issued to an assistant (ADR-0007): its account and
// the assistant's name for the refinement log.
type OAuth func(ctx context.Context, token string) (accountID, assistant string, expires time.Time, err error)

// SetOAuth lets /mcp take assistants' OAuth tokens besides account tokens; a
// 401 then points to the protected resource metadata.
func (s *Server) SetOAuth(verify OAuth, metadataURL string) {
	s.oauth, s.metadataURL = verify, metadataURL
}

const (
	extraAccount   = "account"
	extraAssistant = "assistant"
)

// verify accepts an account token (lib_…) or an assistant's access token.
func (s *Server) verify(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	if strings.HasPrefix(token, "lib_") {
		a, err := account.Authenticate(s.home, token)
		if err != nil {
			return nil, auth.ErrInvalidToken
		}
		// Account tokens do not expire; they are replaced.
		return &auth.TokenInfo{UserID: a.ID, Expiration: time.Now().Add(time.Hour),
			Extra: map[string]any{extraAccount: a, extraAssistant: ""}}, nil
	}
	if s.oauth == nil {
		return nil, auth.ErrInvalidToken
	}
	id, assistant, exp, err := s.oauth(ctx, token)
	if err != nil {
		return nil, auth.ErrInvalidToken
	}
	a, err := account.Open(s.home, id)
	if err != nil {
		return nil, auth.ErrInvalidToken
	}
	return &auth.TokenInfo{UserID: a.ID, Expiration: exp,
		Extra: map[string]any{extraAccount: a, extraAssistant: assistant}}, nil
}

// Handler is the HTTP handler: /health is open, /mcp needs a token, the rest
// is the web interface when one is set.
func (s *Server) Handler() http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		ti := auth.TokenInfoFromContext(r.Context())
		if ti == nil {
			return nil // unreachable: the middleware rejects such requests
		}
		a, _ := ti.Extra[extraAccount].(*account.Account)
		if a == nil {
			return nil
		}
		srv, err := s.forAccount(a)
		if err != nil {
			return nil
		}
		return srv
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	protected := auth.RequireBearerToken(s.verify, &auth.RequireBearerTokenOptions{ResourceMetadataURL: s.metadataURL})(mcpHandler)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	if s.web != nil {
		mux.Handle("/", s.web)
	}
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Browser-based clients read the 401 to find the authorization server.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Expose-Headers", "WWW-Authenticate, Mcp-Session-Id")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Session-Id, MCP-Protocol-Version, Last-Event-ID")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		protected.ServeHTTP(w, r)
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
	c, err := s.catalog(a)
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
	mcp.AddTool(srv, &mcp.Tool{Name: "update_document",
		Description: "Уточнить документ со слов пользователя: человек, характеристики, дата. Правка помечается как правка ассистента до подтверждения владельцем."}, t.update)
	mcp.AddTool(srv, &mcp.Tool{Name: "get_document",
		Description: "Документ по id: резюме (по умолчанию), суть (essential) или дословный текст (verbatim)."}, t.get)
	s.servers[a.ID] = srv
	return srv, nil
}

// Catalog returns the account's catalog, opened once and shared with the web.
func (s *Server) Catalog(a *account.Account) (*catalog.Catalog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.catalog(a)
}

func (s *Server) catalog(a *account.Account) (*catalog.Catalog, error) {
	if c, ok := s.catalogs[a.ID]; ok {
		return c, nil
	}
	c, err := catalog.Open(a.CatalogPath())
	if err != nil {
		return nil, err
	}
	s.catalogs[a.ID] = c
	return c, nil
}

type tools struct{ cat *catalog.Catalog }

// assistantOf names the assistant behind a call: the OAuth client, or the
// account token when the call came with one.
func assistantOf(req *mcp.CallToolRequest) string {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return ""
	}
	if name, _ := req.Extra.TokenInfo.Extra[extraAssistant].(string); name != "" {
		return name
	}
	return "постоянный токен"
}

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
	Attrs    []string `json:"attrs,omitempty" jsonschema:"характеристики «ключ=значение»; должны выполняться все"`
	Batch    int64    `json:"batch,omitempty" jsonschema:"номер пачки: только документы, пришедшие в ней"`
	Missing  []string `json:"missing,omitempty" jsonschema:"характеристики, которых может не быть: date, person или ключ характеристики. Без фильтра по ней — только документы без неё; с фильтром — подходящие по фильтру или без неё"`
	Offset   int      `json:"offset,omitempty" jsonschema:"сколько первых документов пропустить: next_offset из прошлого ответа"`
	Limit    int      `json:"limit,omitempty" jsonschema:"сколько документов вернуть, до 50 (по умолчанию 20)"`
}

type docOut struct {
	ID          string              `json:"id"`
	Date        string              `json:"date,omitempty"`
	DatePartial string              `json:"date_partial,omitempty"`
	Persons     []string            `json:"persons"`
	Pages       int                 `json:"pages"`
	Title       string              `json:"title,omitempty"`
	Summary     string              `json:"summary"`
	Attrs       map[string][]string `json:"attrs,omitempty"`
	Batches     []int64             `json:"batches,omitempty"`
	Review      []string            `json:"review"`
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
	var attrs []catalog.Attr
	for _, s := range in.Attrs {
		a, err := catalog.ParseAttr(s)
		if err != nil {
			return nil, searchOut{}, err
		}
		attrs = append(attrs, a)
	}
	if in.Offset < 0 {
		return nil, searchOut{}, fmt.Errorf("offset не может быть отрицательным")
	}
	hits, total, err := t.cat.Search(ctx, catalog.SearchQuery{
		Text: in.Query, Person: in.Person, DateFrom: in.DateFrom, DateTo: in.DateTo,
		Attrs: attrs, Batch: in.Batch, Missing: in.Missing, Offset: in.Offset, Limit: in.Limit,
	})
	if err != nil {
		return nil, searchOut{}, err
	}
	out := searchOut{Total: total, Documents: []docOut{}}
	for _, h := range hits {
		out.Documents = append(out.Documents, docOut{ID: h.DocID, Date: h.Date, DatePartial: h.Partial, Persons: nonNil(h.Persons),
			Pages: h.Pages, Title: h.Title, Summary: h.Summary, Attrs: h.Attrs, Batches: h.Batches, Review: nonNil(h.Review)})
	}
	if next := in.Offset + len(hits); len(hits) > 0 && next < total {
		out.NextOffset = next
	}
	return nil, out, nil
}

// --- update_document ---------------------------------------------------------

type updateIn struct {
	ID          string   `json:"id" jsonschema:"id документа"`
	Reason      string   `json:"reason" jsonschema:"почему: что сказал пользователь, его словами"`
	AddPersons  []string `json:"add_persons,omitempty" jsonschema:"коды людей из list_persons, к которым относится документ"`
	DropPersons []string `json:"remove_persons,omitempty" jsonschema:"коды людей, к которым документ не относится"`
	AddAttrs    []string `json:"add_attrs,omitempty" jsonschema:"характеристики «ключ=значение»"`
	DropAttrs   []string `json:"remove_attrs,omitempty" jsonschema:"что убрать: «ключ=значение» — одно значение, «ключ» — все значения ключа"`
	Date        string   `json:"date,omitempty" jsonschema:"дата создания: ГГГГ-ММ-ДД, ГГГГ-ММ, ГГГГ или --ММ-ДД; none — даты у документа нет"`
	ClearDate   bool     `json:"clear_date,omitempty" jsonschema:"убрать уточнённую дату: вернётся найденная автоматически"`
}

type updateOut struct {
	ID          string              `json:"id"`
	Date        string              `json:"date,omitempty"`
	DatePartial string              `json:"date_partial,omitempty"`
	DateSource  string              `json:"date_source,omitempty"`
	Persons     []string            `json:"persons"`
	Attrs       map[string][]string `json:"attrs,omitempty"`
	Review      []string            `json:"review"`
}

func (t *tools) update(ctx context.Context, req *mcp.CallToolRequest, in updateIn) (*mcp.CallToolResult, updateOut, error) {
	if strings.TrimSpace(in.Reason) == "" {
		return nil, updateOut{}, fmt.Errorf("reason обязателен: что сказал пользователь")
	}
	if in.Date != "" && in.ClearDate {
		return nil, updateOut{}, fmt.Errorf("date и clear_date вместе не задаются")
	}
	ch := catalog.DocChange{Link: in.AddPersons, Unlink: in.DropPersons, ClearDate: in.ClearDate, Actor: assistantOf(req)}
	for _, s := range in.AddAttrs {
		a, err := catalog.ParseAttr(s)
		if err != nil {
			return nil, updateOut{}, err
		}
		ch.Attrs = append(ch.Attrs, a)
	}
	for _, s := range in.DropAttrs {
		k, v, _ := strings.Cut(s, "=")
		a := catalog.Attr{Key: catalog.AttrKey(k), Value: strings.TrimSpace(v)}
		if a.Key == "" {
			return nil, updateOut{}, fmt.Errorf("remove_attrs: пустой ключ в %q", s)
		}
		ch.Unset = append(ch.Unset, a)
	}
	switch in.Date {
	case "":
	case "none":
		none := ""
		ch.Date = &none
	default:
		d := in.Date
		ch.Date = &d
	}
	now := time.Now()
	if err := t.cat.ChangeDocument(ctx, in.ID, ch, catalog.OriginAssistant, strings.TrimSpace(in.Reason), now); err != nil {
		if errors.Is(err, catalog.ErrNoDocument) {
			return nil, updateOut{}, fmt.Errorf("документ %q не найден", in.ID)
		}
		return nil, updateOut{}, err
	}
	if err := index.Doc(ctx, t.cat, in.ID, now); err != nil {
		return nil, updateOut{}, err
	}
	out := updateOut{ID: in.ID}
	var err error
	var source string
	if out.Date, out.DatePartial, source, err = t.cat.DocDateOf(ctx, in.ID); err != nil {
		return nil, updateOut{}, err
	}
	out.DateSource = catalog.DateSourceLabel(source)
	persons, err := t.cat.DocPersons(ctx, in.ID)
	if err != nil {
		return nil, updateOut{}, err
	}
	out.Persons = nonNil(persons)
	if out.Attrs, err = t.cat.DocAttrs(ctx, in.ID); err != nil {
		return nil, updateOut{}, err
	}
	review, err := t.cat.DocReview(ctx, in.ID)
	if err != nil {
		return nil, updateOut{}, err
	}
	out.Review = nonNil(review)
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
	ID          string              `json:"id"`
	Date        string              `json:"date,omitempty"`
	DatePartial string              `json:"date_partial,omitempty"`
	DateSource  string              `json:"date_source,omitempty"`
	Persons     []string            `json:"persons"`
	Pages       int                 `json:"pages"`
	Title       string              `json:"title,omitempty"`
	Layer       string              `json:"layer"`
	Text        string              `json:"text"`
	Attrs       map[string][]string `json:"attrs,omitempty"`
	Batches     []int64             `json:"batches,omitempty"`
	Review      []string            `json:"review"`
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
	date, partial, source, err := t.cat.DocDateOf(ctx, d.ID)
	if err != nil {
		return nil, getOut{}, err
	}
	review, err := t.cat.DocReview(ctx, d.ID)
	if err != nil {
		return nil, getOut{}, err
	}
	attrs, err := t.cat.DocAttrs(ctx, d.ID)
	if err != nil {
		return nil, getOut{}, err
	}
	batches, err := t.cat.DocBatches(ctx, d.ID)
	if err != nil {
		return nil, getOut{}, err
	}
	out := getOut{ID: d.ID, Date: date, DatePartial: partial, DateSource: catalog.DateSourceLabel(source), Persons: nonNil(persons),
		Pages: d.LastPage - d.FirstPage + 1, Title: tx.Title, Layer: layer, Attrs: attrs, Batches: batches, Review: nonNil(review)}
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
