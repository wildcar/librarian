package web

import (
	"net/http"
	"strings"

	"github.com/wildcar/librarian/internal/catalog"
)

// noPerson is the inbox reason for a document linked to nobody; the other
// reasons come from catalog.DocReview.
const noPerson = "человек: не указан"

type inboxDoc struct {
	catalog.ShelfDoc
	Reasons []string
}

type inboxKind struct {
	Name  string
	Count int
}

// inbox gathers on one page everything that waits for the owner: documents
// without a person and every open check of catalog.DocReview. A kind is the
// part of a reason before the colon; ?kind= keeps documents of one kind.
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
		reasons, err := v.Cat.DocReview(ctx, d.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		if len(d.Persons) == 0 {
			reasons = append([]string{noPerson}, reasons...)
		}
		if len(reasons) == 0 {
			continue
		}
		all = append(all, inboxDoc{ShelfDoc: d, Reasons: reasons})
		seen := map[string]bool{}
		for _, rs := range reasons {
			k := reasonKind(rs)
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
				if reasonKind(rs) == want {
					shown = append(shown, d)
					break
				}
			}
		}
	} else {
		want = ""
	}
	s.render(w, "inbox", map[string]any{"V": v, "Docs": shown, "Kinds": kinds, "Kind": want, "Total": len(all)})
}

func reasonKind(reason string) string {
	k, _, _ := strings.Cut(reason, ":")
	return k
}
