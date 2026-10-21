package intake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wildcar/librarian/internal/catalog"
)

// Stored is a file kept in the account's store.
type Stored struct {
	Name   string
	SHA256 string
	New    bool // the original was new to the account
	Size   int64
}

// Store keeps an uploaded file in the account's own store, <root>/files,
// since it has no other source (ADR-0004), and records it like a folder file.
// The file is named by its content, so the same upload twice is one file.
func Store(ctx context.Context, cat *catalog.Catalog, root, name string, r io.Reader, maxBytes int64) (Stored, error) {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if reason := skipReason(name); reason != "" {
		return Stored{}, fmt.Errorf("%s: %s", name, reason)
	}
	dir := filepath.Join(root, "files")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Stored{}, err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return Stored{}, err
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, maxBytes+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Stored{}, fmt.Errorf("%s: %w", name, err)
	}
	if n > maxBytes {
		return Stored{}, fmt.Errorf("%s: больше %d МБ", name, maxBytes>>20)
	}
	if n == 0 {
		return Stored{}, fmt.Errorf("%s: пустой файл", name)
	}
	sha := hex.EncodeToString(h.Sum(nil))
	ext := strings.ToLower(filepath.Ext(name))
	if len(ext) > 10 || strings.ContainsAny(ext, " /") {
		ext = ""
	}
	sub := filepath.Join(dir, sha[:2])
	if err := os.MkdirAll(sub, 0o700); err != nil {
		return Stored{}, err
	}
	final := filepath.Join(sub, sha+ext)
	if err := os.Rename(tmp.Name(), final); err != nil {
		return Stored{}, err
	}
	mime, err := ContentMIME(final)
	if err != nil {
		return Stored{}, err
	}
	st, err := os.Stat(final)
	if err != nil {
		return Stored{}, err
	}
	isNew, err := cat.RecordSeen(ctx, catalog.Seen{URI: URI(final), SHA256: sha, Size: n, MTime: st.ModTime(),
		MIME: mime, ExtMIME: ExtMIME(name), At: time.Now()})
	if err != nil {
		return Stored{}, err
	}
	return Stored{Name: name, SHA256: sha, New: isNew, Size: n}, nil
}
