package account

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Roots are the server folders the account may take documents from through
// the web (ADR-0007); the administrator sets them, the web never shows
// anything outside them.
func (a *Account) rootsPath() string { return filepath.Join(a.root, "roots") }

// Roots lists the allowed folders, as absolute paths.
func (a *Account) Roots() ([]string, error) {
	b, err := os.ReadFile(a.rootsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out, nil
}

// AddRoot allows a folder; it must exist.
func (a *Account) AddRoot(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	st, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("root %s: %w", abs, err)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("root %s: not a directory", abs)
	}
	rs, err := a.Roots()
	if err != nil {
		return "", err
	}
	if !slices.Contains(rs, abs) {
		rs = append(rs, abs)
	}
	return abs, a.writeRoots(rs)
}

// RemoveRoot forbids a folder again.
func (a *Account) RemoveRoot(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rs, err := a.Roots()
	if err != nil {
		return err
	}
	i := slices.Index(rs, filepath.Clean(abs))
	if i < 0 {
		return fmt.Errorf("%s is not an allowed folder", abs)
	}
	return a.writeRoots(slices.Delete(rs, i, i+1))
}

func (a *Account) writeRoots(rs []string) error {
	return os.WriteFile(a.rootsPath(), []byte(strings.Join(rs, "\n")+"\n"), 0o600)
}

// ErrOutside is a path outside every allowed folder.
var ErrOutside = errors.New("not inside an allowed folder")

// Within resolves a path (symbolic links included) and returns it with the
// allowed folder it lies in; a path outside them all is ErrOutside.
func (a *Account) Within(path string) (resolved, root string, err error) {
	rs, err := a.Roots()
	if err != nil {
		return "", "", err
	}
	if !filepath.IsAbs(path) {
		return "", "", ErrOutside
	}
	p, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", "", ErrOutside
	}
	for _, r := range rs {
		rr, err := filepath.EvalSymlinks(r)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rr, p)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return p, r, nil
		}
	}
	return "", "", ErrOutside
}
