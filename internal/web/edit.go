package web

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/index"
)

// personChoice is a person of the account as the document page offers it.
type personChoice struct {
	Code, Label string
	Checked     bool
	Assistant   bool // linked by an assistant, not yet confirmed
}

func personChoices(ctx context.Context, cat *catalog.Catalog, docID string, persons []catalog.Person) ([]personChoice, error) {
	links, err := cat.DocLinks(ctx, docID)
	if err != nil {
		return nil, err
	}
	method := map[string]string{}
	for _, l := range links {
		method[l.Code] = l.Method
	}
	out := make([]personChoice, len(persons))
	for i, p := range persons {
		m, ok := method[p.Code]
		out[i] = personChoice{Code: p.Code, Label: p.Label, Checked: ok, Assistant: m == "assistant"}
	}
	return out, nil
}

// nobodysByChoice says whether persons were ruled out for a document by
// hand, so that having no person is a decision rather than a gap.
func nobodysByChoice(ctx context.Context, cat *catalog.Catalog, docID string) (bool, error) {
	rej, err := cat.DocRejected(ctx, docID)
	return len(rej.Persons) > 0, err
}

// editPersons sets whose the document is, as the owner ticked it: the ticked
// persons become the owner's links, the others are ruled out, so batches and
// rules never link them again. Nobody ticked says the document is nobody's.
func (s *Server) editPersons(w http.ResponseWriter, r *http.Request, v viewer) {
	ctx, id := r.Context(), r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "неверная форма", http.StatusBadRequest)
		return
	}
	persons, err := v.Cat.Persons(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	ticked := map[string]bool{}
	for _, c := range r.Form["person"] {
		ticked[c] = true
	}
	var ch catalog.DocChange
	for _, p := range persons {
		if ticked[p.Code] {
			ch.Link = append(ch.Link, p.Code)
			delete(ticked, p.Code)
		} else {
			ch.Unlink = append(ch.Unlink, p.Code)
		}
	}
	if len(ticked) > 0 {
		s.showDocument(w, r, v, id, docForm{Edit: "person", Error: "Такого человека нет в библиотеке.", Reason: r.FormValue("reason")})
		return
	}
	s.change(w, r, v, id, "person", ch)
}

// editDate sets the document's date, says it has none, or gives the date
// back to the library.
func (s *Server) editDate(w http.ResponseWriter, r *http.Request, v viewer) {
	id := r.PathValue("id")
	var ch catalog.DocChange
	switch {
	case r.FormValue("clear") != "":
		ch.ClearDate = true
	case r.FormValue("none") != "":
		none := ""
		ch.Date = &none
	default:
		typed := strings.TrimSpace(r.FormValue("date"))
		d, err := parseDate(typed)
		if err != nil {
			s.showDocument(w, r, v, id, docForm{Edit: "date", Error: err.Error(), Date: typed, Reason: r.FormValue("reason")})
			return
		}
		ch.Date = &d
	}
	s.change(w, r, v, id, "date", ch)
}

// change applies the owner's refinement, reindexes the document and shows it.
func (s *Server) change(w http.ResponseWriter, r *http.Request, v viewer, id, form string, ch catalog.DocChange) {
	ctx, now := r.Context(), s.cfg.Now()
	if _, _, err := v.Cat.GetDocument(ctx, id); err != nil {
		w.WriteHeader(http.StatusNotFound)
		s.render(w, "message", map[string]any{"V": v, "Title": "Нет такого документа", "Text": "В вашей библиотеке нет документа " + id + "."})
		return
	}
	reason := strings.TrimSpace(r.FormValue("reason"))
	if err := v.Cat.ChangeDocument(ctx, id, ch, catalog.OriginOwner, reason, now); err != nil {
		s.showDocument(w, r, v, id, docForm{Edit: form, Error: err.Error(), Date: r.FormValue("date"), Reason: reason})
		return
	}
	if err := index.Doc(ctx, v.Cat, id, now); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, localPath(r.FormValue("back"), "/doc/"+id), http.StatusSeeOther)
}

var (
	isoDate = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}|\d{4}-\d{2}|\d{4}|--\d{2}-\d{2})$`)
	ruDay   = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})\.(\d{4})$`)
	ruMonth = regexp.MustCompile(`^(\d{1,2})\.(\d{4})$`)
	ruNoYr  = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})$`)
)

// parseDate reads a date as the owner types it, ДД.ММ.ГГГГ, ММ.ГГГГ, ГГГГ
// or ДД.ММ (no year), or as stored, and returns it as stored.
func parseDate(s string) (string, error) {
	var d string
	switch {
	case isoDate.MatchString(s):
		d = s
	case ruDay.MatchString(s):
		m := ruDay.FindStringSubmatch(s)
		d = fmt.Sprintf("%s-%02s-%02s", m[3], m[2], m[1])
	case ruMonth.MatchString(s):
		m := ruMonth.FindStringSubmatch(s)
		d = fmt.Sprintf("%s-%02s", m[2], m[1])
	case ruNoYr.MatchString(s):
		m := ruNoYr.FindStringSubmatch(s)
		d = fmt.Sprintf("--%02s-%02s", m[2], m[1])
	default:
		return "", fmt.Errorf("Дата %q: напишите ДД.ММ.ГГГГ, ММ.ГГГГ, ГГГГ или ДД.ММ, если год неизвестен.", s)
	}
	d = strings.ReplaceAll(d, " ", "0")
	if _, err := catalog.CheckDate(d); err != nil {
		return "", fmt.Errorf("Такой даты нет: %s.", s)
	}
	return d, nil
}

// typedDate shows a stored date the way parseDate reads it back.
func typedDate(date, partial string) string {
	switch {
	case date != "":
		return ruDate(date)
	case strings.HasPrefix(partial, "--") && len(partial) == 7:
		return partial[5:7] + "." + partial[2:4]
	case len(partial) == 7:
		return partial[5:7] + "." + partial[:4]
	}
	return partial
}
