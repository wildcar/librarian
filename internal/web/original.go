package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"time"
	"unicode/utf8"
)

// shownTypes are the originals a browser shows itself and that cannot run
// a script on this site; anything else (HTML, SVG, office files) is only
// downloaded.
var shownTypes = map[string]bool{
	"application/pdf": true, "image/jpeg": true, "image/png": true, "image/gif": true,
	"image/webp": true, "text/plain": true,
}

// original serves the file a document was cut from. The path comes from the
// account's own catalog, and the file is served only while its content is
// still the original's, so a folder file changed or replaced since intake
// is never shown as the document's source.
func (s *Server) original(w http.ResponseWriter, r *http.Request, v viewer) {
	ctx, id := r.Context(), r.PathValue("id")
	d, _, err := v.Cat.GetDocument(ctx, id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		s.render(w, "message", map[string]any{"V": v, "Title": "Нет такого документа", "Text": "В вашей библиотеке нет документа " + id + "."})
		return
	}
	files, err := v.Cat.SourceFiles(ctx, d.SHA256)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, f := range files {
		fh, err := openOriginal(f.File, d.SHA256)
		if err != nil {
			continue
		}
		defer fh.Close()
		ctype, err := v.Cat.OriginalMIME(ctx, d.SHA256)
		if err != nil {
			s.fail(w, err)
			return
		}
		name := f.Name
		if name == "" {
			name = path.Base(f.File)
		}
		disp := "inline"
		switch {
		case !shownTypes[ctype]:
			ctype, disp = "application/octet-stream", "attachment"
		case ctype == "text/plain":
			ctype += "; charset=" + textCharset(fh)
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": name}))
		w.Header().Set("Cache-Control", "private, no-cache")
		http.ServeContent(w, r, "", time.Time{}, fh)
		return
	}
	w.WriteHeader(http.StatusNotFound)
	s.render(w, "message", map[string]any{"V": v, "Title": "Файл недоступен",
		"Text": "Файла, из которого получен документ, больше нет на месте, или он изменился с тех пор."})
}

// openOriginal opens a file when its content hashes to sha, positioned at
// its start.
func openOriginal(name, sha string) (*os.File, error) {
	if name == "" {
		return nil, os.ErrNotExist
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		f.Close()
		return nil, err
	}
	if hex.EncodeToString(h.Sum(nil)) != sha {
		f.Close()
		return nil, os.ErrNotExist
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// textCharset tells UTF-8 from Windows-1251, the two encodings plain text
// originals are read in (internal/pages), and rewinds the file.
func textCharset(f *os.File) string {
	defer f.Seek(0, io.SeekStart)
	const limit = 1 << 20
	b, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return "utf-8"
	}
	// The cut at the limit may split the last rune; drop up to three bytes.
	cut := 0
	if len(b) == limit {
		cut = 3
	}
	for i := 0; i <= cut; i++ {
		if utf8.Valid(b[:len(b)-i]) {
			return "utf-8"
		}
	}
	return "windows-1251"
}

// fileAnchor opens a PDF on the document's first page; other files have one
// page or none to jump to.
func fileAnchor(ctype string, first int) string {
	if ctype == "application/pdf" && first > 1 {
		return "#page=" + strconv.Itoa(first)
	}
	return ""
}
