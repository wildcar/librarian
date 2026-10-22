package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/index"
)

// noPerson is the inbox reason for a document linked to nobody; the other
// reasons come from catalog.DocReviewItems.
const noPerson = "человек: не указан"

type inboxDoc struct {
	catalog.ShelfDoc
	Reasons  []reviewRow
	Assisted string // who refined it and why, while that waits for the owner
}

type inboxKind struct {
	Name  string
	Count int
}

// inbox gathers on one page everything that waits for the owner: documents
// without a person and every open check of catalog.DocReviewItems. A kind
// is the part of a reason before the colon; ?kind= keeps documents of one kind.
func (s *Server) inbox(w http.ResponseWriter, r *http.Request, v viewer) {
	ctx := r.Context()
	docs, err := v.Cat.Shelf(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	want := r.URL.Query().Get("kind")
	counts := map[string]int{}
	var kinds []inboxKind
	var all []inboxDoc
	for _, d := range docs {
		reasons, err := v.Cat.DocReviewItems(ctx, d.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		if len(d.Persons) == 0 {
			reasons = append([]catalog.ReviewItem{{Text: noPerson}}, reasons...)
		}
		if len(reasons) == 0 {
			continue
		}
		item := inboxDoc{ShelfDoc: d}
		if item.Reasons, err = reviewRows(ctx, v.Cat, d.ID, reasons); err != nil {
			s.fail(w, err)
			return
		}
		if item.Assisted, err = assisted(ctx, v.Cat, d.ID, reasons); err != nil {
			s.fail(w, err)
			return
		}
		all = append(all, item)
		seen := map[string]bool{}
		for _, rs := range reasons {
			k := reasonKind(rs.Text)
			if seen[k] {
				continue
			}
			seen[k] = true
			if _, ok := counts[k]; !ok {
				kinds = append(kinds, inboxKind{Name: k})
			}
			counts[k]++
		}
	}
	for i := range kinds {
		kinds[i].Count = counts[kinds[i].Name]
	}
	shown := all
	if _, ok := counts[want]; ok {
		shown = nil
		for _, d := range all {
			for _, rs := range d.Reasons {
				if reasonKind(rs.Text) == want {
					shown = append(shown, d)
					break
				}
			}
		}
	} else {
		want = ""
	}
	back := "/inbox"
	if want != "" {
		back += "?kind=" + url.QueryEscape(want)
	}
	for _, d := range shown {
		for i := range d.Reasons {
			d.Reasons[i].Back = back
		}
	}
	s.render(w, "inbox", map[string]any{"V": v, "Docs": shown, "Kinds": kinds, "Kind": want, "Total": len(all)})
}

func reasonKind(reason string) string {
	k, _, _ := strings.Cut(reason, ":")
	return k
}

// reviewRow is a review item as a page shows it, with its buttons.
type reviewRow struct {
	catalog.ReviewItem
	DocID    string
	Back     string // where the buttons return
	PageLink string // for a doubted page: the original at that page
}

func reviewRows(ctx context.Context, cat *catalog.Catalog, docID string, items []catalog.ReviewItem) ([]reviewRow, error) {
	out := make([]reviewRow, len(items))
	var d catalog.Document
	ctype := ""
	for i, it := range items {
		out[i] = reviewRow{ReviewItem: it, DocID: docID, Back: "/doc/" + docID}
		if it.Fix != catalog.FixPage {
			continue
		}
		if ctype == "" {
			var err error
			if d, _, err = cat.GetDocument(ctx, docID); err != nil {
				return nil, err
			}
			if ctype, err = cat.OriginalMIME(ctx, d.SHA256); err != nil {
				return nil, err
			}
		}
		out[i].PageLink = "/doc/" + docID + "/file"
		if ctype == "application/pdf" {
			out[i].PageLink += "#page=" + strconv.Itoa(d.FirstPage+it.Page-1)
		}
	}
	return out, nil
}

// assisted names the assistant behind the latest refinement and the user's
// words it gave, when a refinement waits for the owner.
func assisted(ctx context.Context, cat *catalog.Catalog, docID string, reasons []catalog.ReviewItem) (string, error) {
	waiting := false
	for _, r := range reasons {
		waiting = waiting || r.Fix == catalog.FixAssistant
	}
	if !waiting {
		return "", nil
	}
	changes, err := cat.DocChanges(ctx, docID)
	if err != nil {
		return "", err
	}
	for i := len(changes) - 1; i >= 0; i-- {
		if ch := changes[i]; ch.Origin == "assistant" {
			who := ch.Actor
			if who == "" {
				who = "ассистент"
			}
			if ch.Reason == "" {
				return who, nil
			}
			return who + ": «" + ch.Reason + "»", nil
		}
	}
	return "", nil
}

// settle is a button of the inbox or the document page: the owner confirms
// a disputed split or a doubted page, or accepts or reverts an assistant's
// refinements. It returns to the page the button was on.
func (s *Server) settle(w http.ResponseWriter, r *http.Request, v viewer) {
	ctx, id, now := r.Context(), r.PathValue("id"), s.cfg.Now()
	if _, _, err := v.Cat.GetDocument(ctx, id); err != nil {
		w.WriteHeader(http.StatusNotFound)
		s.render(w, "message", map[string]any{"V": v, "Title": "Нет такого документа", "Text": "В вашей библиотеке нет документа " + id + "."})
		return
	}
	var err error
	switch r.FormValue("fix") {
	case catalog.FixSplit:
		err = v.Cat.ConfirmSplit(ctx, id, now)
	case catalog.FixPage:
		page, perr := strconv.Atoi(r.FormValue("page"))
		if perr != nil || page < 1 {
			http.Error(w, "нет номера страницы", http.StatusBadRequest)
			return
		}
		err = v.Cat.ConfirmPage(ctx, id, page, now)
	case catalog.FixAssistant:
		if r.FormValue("revert") != "" {
			_, err = v.Cat.RevertAssistant(ctx, id)
		} else {
			_, err = v.Cat.ConfirmAssistant(ctx, id)
		}
		if err == nil {
			err = index.Doc(ctx, v.Cat, id, now)
		}
	default:
		http.Error(w, "неизвестное действие", http.StatusBadRequest)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, localPath(r.FormValue("back"), "/doc/"+id), http.StatusSeeOther)
}

// localPath keeps a return address on this site.
func localPath(p, fallback string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return fallback
	}
	return p
}
