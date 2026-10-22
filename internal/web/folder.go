package web

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/pipeline"
)

// A server folder as a source (ADR-0007): the owner browses only inside the
// folders the administrator allowed for the account and picks one; the
// worker records its files as a batch and processes them. Files stay where
// they are.

type dirEntry struct{ Name, Path string }

// toPipeline turns the form's labels into the worker's.
func (l *labels) toPipeline() pipeline.Labels {
	p := pipeline.Labels{Persons: l.Persons, Attrs: l.attrs, Label: l.Label}
	if l.NewName != "" {
		p.New = &catalog.Person{Code: l.NewCode, FullName: l.NewName, Label: l.NewLabel, BirthDate: l.NewBirth}
	}
	return p
}

func (s *Server) folderPage(w http.ResponseWriter, r *http.Request, v viewer, path string, l *labels, problem string) {
	roots, err := v.Acc.Roots()
	if err != nil {
		s.fail(w, err)
		return
	}
	ps, err := v.Cat.Persons(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	chosen := map[string]bool{}
	for _, c := range l.Persons {
		chosen[c] = true
	}
	data := map[string]any{"V": v, "Roots": roots, "Persons": ps, "Chosen": chosen, "L": l, "Error": problem}
	if path == "" {
		var dirs []dirEntry
		for _, r := range roots {
			dirs = append(dirs, dirEntry{Name: r, Path: r})
		}
		data["Dirs"] = dirs
		s.render(w, "folder", data)
		return
	}
	resolved, root, err := v.Acc.Within(path)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		data["Error"] = "Эта папка вне разрешённых."
		s.render(w, "folder", data)
		return
	}
	data["Path"] = resolved
	// Breadcrumbs from the allowed folder down.
	rr, _ := filepath.EvalSymlinks(root)
	crumbs := []dirEntry{{Name: root, Path: rr}}
	if rel, _ := filepath.Rel(rr, resolved); rel != "." {
		p := rr
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			p = filepath.Join(p, part)
			crumbs = append(crumbs, dirEntry{Name: part, Path: p})
		}
	}
	data["Crumbs"] = crumbs
	es, err := os.ReadDir(resolved)
	if err != nil {
		data["Denied"] = true
		s.render(w, "folder", data)
		return
	}
	var dirs []dirEntry
	files := 0
	for _, e := range es {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, dirEntry{Name: e.Name(), Path: filepath.Join(resolved, e.Name())})
		} else {
			files++
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	data["Dirs"], data["Files"] = dirs, files
	s.render(w, "folder", data)
}

func (s *Server) folder(w http.ResponseWriter, r *http.Request, v viewer) {
	s.folderPage(w, r, v, r.URL.Query().Get("path"), &labels{}, "")
}

func (s *Server) takeFolder(w http.ResponseWriter, r *http.Request, v viewer) {
	r.ParseForm()
	f := r.PostForm
	l := &labels{Persons: f["person"], NewCode: f.Get("new_code"), NewName: f.Get("new_name"),
		NewLabel: strings.TrimSpace(f.Get("new_label")), NewBirth: strings.TrimSpace(f.Get("new_birth")),
		AttrsText: f.Get("attrs"), Label: strings.TrimSpace(f.Get("label"))}
	path := f.Get("path")
	resolved, _, err := v.Acc.Within(path)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		s.folderPage(w, r, v, "", l, "Эта папка вне разрешённых.")
		return
	}
	if err := l.check(r, v.Cat); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		s.folderPage(w, r, v, resolved, l, err.Error())
		return
	}
	if s.cfg.Worker == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		s.folderPage(w, r, v, resolved, l, "Фоновая обработка на сервере выключена: папку сейчас не принять.")
		return
	}
	s.log.Printf("folder: %s takes %s", v.Acc.ID, resolved)
	s.cfg.Worker.IngestFolder(v.Acc.ID, resolved, l.toPipeline())
	http.Redirect(w, r, "/batches", http.StatusSeeOther)
}
