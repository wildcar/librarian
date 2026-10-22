package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/index"
	"github.com/wildcar/librarian/internal/intake"
	"github.com/wildcar/librarian/internal/pipeline"
)

// Upload limits.
const (
	MaxFileBytes   = 100 << 20
	MaxUploadBytes = 1 << 30
)

// labels are the owner's words for an upload, as for `ingest` (ADR-0006).
type labels struct {
	Persons                    []string
	NewCode, NewName, NewLabel string
	NewBirth                   string
	AttrsText, Label           string
	attrs                      []catalog.Attr
}

// check validates the labels before any file is kept: characteristics parse,
// chosen persons exist, a new person's code is free.
func (l *labels) check(r *http.Request, c *catalog.Catalog) error {
	for _, line := range strings.Split(l.AttrsText, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		a, err := catalog.ParseAttr(line)
		if err != nil {
			return fmt.Errorf("характеристика «%s»: нужно «ключ = значение»", strings.TrimSpace(line))
		}
		l.attrs = append(l.attrs, a)
	}
	ps, err := c.Persons(r.Context())
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, p := range ps {
		known[p.Code] = true
	}
	for _, code := range l.Persons {
		if !known[code] {
			return fmt.Errorf("человека с кодом %s нет", code)
		}
	}
	l.NewCode, l.NewName = strings.TrimSpace(l.NewCode), strings.TrimSpace(l.NewName)
	switch {
	case l.NewName == "" && l.NewCode == "":
	case l.NewName == "" || l.NewCode == "":
		return errors.New("для нового человека нужны и код, и полное имя")
	case known[l.NewCode]:
		return fmt.Errorf("код %s уже занят: выберите этого человека в списке", l.NewCode)
	case l.NewBirth != "":
		if _, err := time.Parse("2006-01-02", l.NewBirth); err != nil {
			return errors.New("дата рождения: нужен формат ГГГГ-ММ-ДД")
		}
	}
	return nil
}

func (s *Server) uploadForm(w http.ResponseWriter, r *http.Request, v viewer) {
	s.renderUpload(w, r, v, &labels{}, "")
}

func (s *Server) renderUpload(w http.ResponseWriter, r *http.Request, v viewer, l *labels, problem string) {
	ps, err := v.Cat.Persons(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	chosen := map[string]bool{}
	for _, c := range l.Persons {
		chosen[c] = true
	}
	s.render(w, "upload", map[string]any{"V": v, "Persons": ps, "Chosen": chosen, "L": l, "Error": problem,
		"MaxFileMB": MaxFileBytes >> 20})
}

type uploaded struct {
	Name, Problem string
	New           bool
}

// upload streams the files into the account's store and makes them one batch
// with the owner's labels; the worker then takes them to search.
func (s *Server) upload(w http.ResponseWriter, r *http.Request, v viewer) {
	ctx := r.Context()
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		s.renderUpload(w, r, v, &labels{}, "Форма пришла не целиком, попробуйте ещё раз.")
		return
	}
	l := &labels{}
	checked := false
	var files []uploaded
	var shas []string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.log.Printf("upload: %v", err)
			files = append(files, uploaded{Name: "…", Problem: "загрузка оборвалась или превысила " + fmt.Sprint(MaxUploadBytes>>20) + " МБ"})
			break
		}
		if part.FileName() == "" {
			val, _ := io.ReadAll(io.LimitReader(part, 64<<10))
			switch part.FormName() {
			case "person":
				l.Persons = append(l.Persons, string(val))
			case "new_code":
				l.NewCode = string(val)
			case "new_name":
				l.NewName = string(val)
			case "new_label":
				l.NewLabel = strings.TrimSpace(string(val))
			case "new_birth":
				l.NewBirth = strings.TrimSpace(string(val))
			case "attrs":
				l.AttrsText = string(val)
			case "label":
				l.Label = strings.TrimSpace(string(val))
			}
			continue
		}
		// The form puts the labels before the files: check them before
		// anything is kept.
		if !checked {
			if err := l.check(r, v.Cat); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				s.renderUpload(w, r, v, l, err.Error())
				return
			}
			checked = true
		}
		st, err := intake.Store(ctx, v.Cat, v.Acc.Root(), part.FileName(), part, MaxFileBytes)
		if err != nil {
			files = append(files, uploaded{Name: part.FileName(), Problem: err.Error()})
			continue
		}
		files = append(files, uploaded{Name: st.Name, New: st.New})
		shas = append(shas, st.SHA256)
	}
	if len(shas) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		msg := "Не выбрано ни одного файла."
		if len(files) > 0 {
			msg = "Ни один файл не сохранился: " + files[0].Name + " — " + files[0].Problem
		}
		s.renderUpload(w, r, v, l, msg)
		return
	}

	now := s.cfg.Now()
	persons := append([]string{}, l.Persons...)
	if l.NewName != "" {
		p := catalog.Person{Code: l.NewCode, FullName: l.NewName, Label: l.NewLabel, BirthDate: l.NewBirth}
		if err := v.Cat.AddPerson(ctx, p, now); err != nil {
			s.fail(w, err)
			return
		}
		persons = append(persons, l.NewCode)
	}
	b := catalog.Batch{Kind: "upload", Label: l.Label, Persons: persons, Attrs: l.attrs}
	id, err := v.Cat.CreateBatch(ctx, b, shas, now)
	if err != nil {
		s.fail(w, err)
		return
	}
	// Files already in the library get the labels at once.
	if len(persons) > 0 || len(l.attrs) > 0 {
		if _, err := index.Run(ctx, v.Cat, now); err != nil {
			s.fail(w, err)
			return
		}
	}
	s.log.Printf("upload: %s batch %d, %d files", v.Acc.ID, id, len(shas))
	if s.cfg.Worker != nil {
		s.cfg.Worker.Kick()
	}
	s.render(w, "uploaded", map[string]any{"V": v, "Batch": id, "Files": files})
}

// --- batches -------------------------------------------------------------------

type batchRow struct {
	catalog.Batch
	P       catalog.BatchProgress
	Persons []string
}

func (s *Server) batches(w http.ResponseWriter, r *http.Request, v viewer) {
	ctx := r.Context()
	bs, err := v.Cat.Batches(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	prog, err := v.Cat.BatchProgresses(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	ps, err := v.Cat.Persons(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	names := map[string]string{}
	for _, p := range ps {
		names[p.Code] = p.Label
	}
	var rows []batchRow
	for _, b := range bs {
		row := batchRow{Batch: b, P: prog[b.ID]}
		for _, c := range b.Persons {
			row.Persons = append(row.Persons, personLabel(c, names))
		}
		rows = append(rows, row)
	}
	data := map[string]any{"V": v, "Batches": rows, "Running": false}
	if s.cfg.Worker != nil {
		st := s.cfg.Worker.Status(v.Acc.ID)
		data["W"], data["Running"] = st, st.State == pipeline.Running
	}
	s.render(w, "batches", data)
}

// process is the owner's «Обработать» for a run over the cost limit.
func (s *Server) process(w http.ResponseWriter, r *http.Request, v viewer) {
	if s.cfg.Worker != nil && s.cfg.Worker.Status(v.Acc.ID).State == pipeline.Held {
		s.log.Printf("pipeline: %s allowed ~$%.2f", v.Email, s.cfg.Worker.Status(v.Acc.ID).EstimateUSD)
		s.cfg.Worker.Approve(v.Acc.ID)
	}
	http.Redirect(w, r, "/batches", http.StatusSeeOther)
}

var stageNames = map[string]string{
	"extract": "распознавание", "split": "разбивка на документы", "digest": "резюме",
	"date": "даты", "describe": "названия и характеристики", "index": "индекс", "folder": "приём папки",
}
