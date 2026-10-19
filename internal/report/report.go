// Package report renders catalog facts for a human. User-facing text is Russian.
package report

import (
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/intake"
)

// Intake prints the result of one intake run.
func Intake(w io.Writer, r *intake.Report) {
	fmt.Fprintf(w, "Папка: %s\n", r.Root)
	fmt.Fprintf(w, "Файлов: %d, из них без изменений с прошлого раза: %d, прочитано: %d, новых оригиналов: %d\n",
		r.Files, r.Unchanged, r.Hashed, r.NewFiles)
	if len(r.Skipped) > 0 {
		keys := sortedKeys(r.Skipped)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s — %d", skipLabel(k), r.Skipped[k]))
		}
		fmt.Fprintf(w, "Пропущено: %s\n", strings.Join(parts, "; "))
	}
	if r.Missing > 0 {
		fmt.Fprintf(w, "Пропали с прошлого раза: %d (в каталоге помечены, не удалены)\n", r.Missing)
	}
	if len(r.Errors) > 0 {
		fmt.Fprintf(w, "Ошибки: %d\n", len(r.Errors))
		for _, e := range r.Errors {
			fmt.Fprintf(w, "  %s\n", e)
		}
	}
	fmt.Fprintf(w, "Время: %s\n", r.Duration.Round(1e7))
}

var skipLabels = map[string]string{
	"os metadata":          "служебные файлы ОС",
	"temporary file":       "временные файлы",
	"sync client metadata": "служебные файлы синхронизации",
	"not a regular file":   "не обычные файлы (ссылки, устройства)",
}

func skipLabel(k string) string {
	if l, ok := skipLabels[k]; ok {
		return l
	}
	return k
}

// Duplicates prints originals seen in more than one place.
func Duplicates(w io.Writer, dups []catalog.Duplicate) {
	if len(dups) == 0 {
		fmt.Fprintln(w, "Дубликатов нет.")
		return
	}
	var wasted int64
	for _, d := range dups {
		wasted += d.Size * int64(len(d.URIs)-1)
	}
	fmt.Fprintf(w, "Дубликатов: %d групп, лишних копий занимают %s\n\n", len(dups), Bytes(wasted))
	for i, d := range dups {
		fmt.Fprintf(w, "%d. %s, %s, sha256 %s…\n", i+1, Bytes(d.Size), d.MIME, d.SHA256[:12])
		for _, u := range d.URIs {
			fmt.Fprintf(w, "   %s\n", Path(u))
		}
	}
}

// Stats prints catalog totals.
func Stats(w io.Writer, s catalog.Stats, mismatches [][3]string) {
	fmt.Fprintf(w, "Оригиналов: %d (%s), мест, где они встречались: %d, из них пропавших: %d\n",
		s.Originals, Bytes(s.Bytes), s.Sources, s.Missing)
	for _, k := range sortedKeys(s.ByMIME) {
		fmt.Fprintf(w, "  %-40s %d\n", k, s.ByMIME[k])
	}
	if len(mismatches) > 0 {
		fmt.Fprintf(w, "Расширение не совпадает с содержимым: %d\n", len(mismatches))
		for _, m := range mismatches {
			fmt.Fprintf(w, "  %s: по расширению %s, по содержимому %s\n", Path(m[0]), m[1], m[2])
		}
	}
}

// Path turns a file:// URI back into a readable path.
func Path(uri string) string {
	p := strings.TrimPrefix(uri, "file://")
	if len(p) > 2 && p[0] == '/' && p[2] == ':' { // /C:/... on Windows
		p = p[1:]
	}
	if u, err := url.PathUnescape(p); err == nil {
		return u
	}
	return p
}

// Bytes formats a size for humans.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d Б", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), []string{"КБ", "МБ", "ГБ", "ТБ"}[exp])
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
