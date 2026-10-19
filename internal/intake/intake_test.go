package intake_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/intake"
)

var (
	pdfBytes  = []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n")
	jpegBytes = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00")
	zipBytes  = []byte("PK\x03\x04\x14\x00\x06\x00\x08\x00\x00\x00!\x00")
)

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func newCatalog(t *testing.T, id string) *catalog.Catalog {
	t.Helper()
	a, err := account.Create(t.TempDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(a.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestFolderLifecycle(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	write(t, filepath.Join(src, "scan.pdf"), pdfBytes)
	write(t, filepath.Join(src, "Папа", "копия скана.pdf"), pdfBytes) // same content, other place
	write(t, filepath.Join(src, "note.txt"), []byte("Прием кардиолога 2026-07-14\n"))
	write(t, filepath.Join(src, "desktop.ini"), []byte("[.ShellClassInfo]"))
	write(t, filepath.Join(src, "~$report.docx"), []byte("lock"))
	write(t, filepath.Join(src, "letter.docx"), zipBytes)
	write(t, filepath.Join(src, "photo.pdf"), jpegBytes) // wrong extension

	c := newCatalog(t, "family")
	rep, err := intake.Folder(ctx, c, src, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Files != 5 || rep.Hashed != 5 || rep.NewFiles != 4 {
		t.Errorf("first run: files=%d hashed=%d new=%d, want 5 5 4", rep.Files, rep.Hashed, rep.NewFiles)
	}
	if rep.Skipped["os metadata"] != 1 || rep.Skipped["temporary file"] != 1 {
		t.Errorf("skipped = %v", rep.Skipped)
	}

	dups, err := c.Duplicates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(dups) != 1 || len(dups[0].URIs) != 2 || dups[0].MIME != "application/pdf" {
		t.Fatalf("duplicates = %+v, want one pdf group of two", dups)
	}

	mm, err := c.MIMEMismatches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(mm) != 1 || mm[0][2] != "image/jpeg" {
		t.Errorf("mismatches = %v, want photo.pdf as image/jpeg", mm)
	}
	st, err := c.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	docx := "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	if st.ByMIME[docx] != 1 {
		t.Errorf("by mime = %v, want one docx", st.ByMIME)
	}

	// Second run: nothing changed, nothing is read again.
	rep, err = intake.Folder(ctx, c, src, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Unchanged != 5 || rep.Hashed != 0 || rep.NewFiles != 0 {
		t.Errorf("second run: unchanged=%d hashed=%d new=%d, want 5 0 0", rep.Unchanged, rep.Hashed, rep.NewFiles)
	}

	// An edited file is read again; a removed copy is flagged, not forgotten.
	later := time.Now().Add(2 * time.Second)
	write(t, filepath.Join(src, "note.txt"), []byte("Прием кардиолога 2026-07-15\n"))
	if err := os.Chtimes(filepath.Join(src, "note.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(src, "Папа", "копия скана.pdf")); err != nil {
		t.Fatal(err)
	}
	rep, err = intake.Folder(ctx, c, src, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Hashed != 1 || rep.NewFiles != 1 || rep.Missing != 1 {
		t.Errorf("third run: hashed=%d new=%d missing=%d, want 1 1 1", rep.Hashed, rep.NewFiles, rep.Missing)
	}
	if dups, _ := c.Duplicates(ctx); len(dups) != 0 {
		t.Errorf("duplicates after removal = %+v, want none", dups)
	}
	st, _ = c.Stats(ctx)
	if st.Sources != 5 || st.Missing != 1 {
		t.Errorf("sources=%d missing=%d, want 5 1 (nothing deleted)", st.Sources, st.Missing)
	}
}

func TestOriginalsUntouched(t *testing.T) {
	src := t.TempDir()
	p := filepath.Join(src, "scan.pdf")
	write(t, p, pdfBytes)
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	c := newCatalog(t, "family")
	if _, err := intake.Folder(context.Background(), c, src, 1); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != string(pdfBytes) {
		t.Fatal("original content changed")
	}
	st, _ := os.Stat(p)
	if !st.ModTime().Equal(old) {
		t.Errorf("original mtime changed: %v", st.ModTime())
	}
	entries, _ := os.ReadDir(src)
	if len(entries) != 1 {
		t.Errorf("intake wrote into the source folder: %d entries", len(entries))
	}
}

func TestAccountsAreIsolated(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	write(t, filepath.Join(src, "a.pdf"), pdfBytes)
	alpha, beta := newCatalog(t, "alpha"), newCatalog(t, "beta")
	if _, err := intake.Folder(ctx, alpha, src, 1); err != nil {
		t.Fatal(err)
	}
	st, err := beta.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Originals != 0 || st.Sources != 0 {
		t.Errorf("account beta sees %d originals after intake into alpha", st.Originals)
	}
}
