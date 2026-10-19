// Package intake walks a folder and records every file in the account catalog:
// content hash, size, type and where it was seen. Originals are only read.
package intake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wildcar/librarian/internal/catalog"
)

// Report summarises one intake run.
type Report struct {
	Root      string
	Files     int            // regular files considered
	Unchanged int            // same path, size and mtime as last time: not re-hashed
	Hashed    int            // read and hashed
	NewFiles  int            // originals the account had never seen
	Skipped   map[string]int // reason -> count
	Errors    []string
	Missing   int64 // sources under Root that disappeared since the last run
	Duration  time.Duration
}

// skipReason returns why a name is not a document, or "".
func skipReason(name string) string {
	low := strings.ToLower(name)
	switch {
	case low == "desktop.ini", low == "thumbs.db", low == ".ds_store":
		return "os metadata"
	case strings.HasPrefix(name, "~$"), strings.HasSuffix(low, ".tmp"), strings.HasSuffix(low, ".part"):
		return "temporary file"
	case strings.HasPrefix(low, ".sync_"), strings.HasPrefix(low, ".owncloudsync"),
		strings.HasPrefix(low, ".nextcloudsync"), low == ".nextcloud":
		return "sync client metadata"
	}
	return ""
}

// URI is the source URI of a local path: file:///C:/dir/file.pdf.
func URI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

type job struct {
	path string
	uri  string
	info fs.FileInfo
}

type result struct {
	job
	sha  string
	mime string
	err  error
}

// Folder records every file under root in the catalog. Workers hash in
// parallel; catalog writes happen on the calling goroutine.
func Folder(ctx context.Context, cat *catalog.Catalog, root string, workers int) (*Report, error) {
	start := time.Now()
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("intake %s: %w", root, err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("intake %s: %w", root, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("intake %s: not a directory", root)
	}
	rep := &Report{Root: abs, Skipped: map[string]int{}}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // an early return must not leave workers blocked
	runID, err := cat.StartRun(ctx, URI(abs), start)
	if err != nil {
		return nil, err
	}
	if workers < 1 {
		workers = 1
	}

	jobs := make(chan job)
	results := make(chan result)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				r := result{job: j}
				r.sha, r.err = hashFile(j.path)
				if r.err == nil {
					r.mime, r.err = ContentMIME(j.path)
				}
				select {
				case results <- r:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Walk on its own goroutine; unchanged files are decided here, the rest go
	// to the workers. The walk owns `w` until walkErr is received.
	type walkState struct {
		files     int
		skipped   map[string]int
		unchanged []string
		errors    []string
	}
	w := &walkState{skipped: map[string]int{}}
	walkErr := make(chan error, 1)
	go func() {
		defer close(jobs)
		walkErr <- filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				w.errors = append(w.errors, fmt.Sprintf("%s: %v", path, err))
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if reason := skipReason(d.Name()); reason != "" {
				w.skipped[reason]++
				return nil
			}
			if !d.Type().IsRegular() {
				w.skipped["not a regular file"]++
				return nil
			}
			info, err := d.Info()
			if err != nil {
				w.errors = append(w.errors, fmt.Sprintf("%s: %v", path, err))
				return nil
			}
			w.files++
			uri := URI(path)
			prev, err := cat.LookupSource(ctx, uri)
			if err != nil {
				return err
			}
			if prev != nil && prev.Size == info.Size() && prev.MTime.Equal(info.ModTime().UTC()) {
				w.unchanged = append(w.unchanged, uri)
				return nil
			}
			select {
			case jobs <- job{path: path, uri: uri, info: info}:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	now := time.Now()
	for r := range results {
		if r.err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", r.path, r.err))
			continue
		}
		rep.Hashed++
		isNew, err := cat.RecordSeen(ctx, catalog.Seen{
			URI: r.uri, SHA256: r.sha, Size: r.info.Size(), MTime: r.info.ModTime(),
			MIME: r.mime, ExtMIME: ExtMIME(r.path), At: now,
		})
		if err != nil {
			return nil, err
		}
		if isNew {
			rep.NewFiles++
		}
	}
	if err := <-walkErr; err != nil {
		return nil, fmt.Errorf("intake %s: %w", root, err)
	}
	rep.Files, rep.Skipped = w.files, w.skipped
	rep.Errors = append(w.errors, rep.Errors...)
	for _, uri := range w.unchanged {
		if err := cat.TouchSource(ctx, uri, now); err != nil {
			return nil, err
		}
	}
	rep.Unchanged = len(w.unchanged)
	// Anything under this root that was not seen in this run has gone away.
	rep.Missing, err = cat.MarkMissing(ctx, URI(abs)+"/", now)
	if err != nil {
		return nil, err
	}
	rep.Duration = time.Since(start)
	err = cat.FinishRun(ctx, catalog.Run{ID: runID, Finished: time.Now(),
		Files: rep.Files, Hashed: rep.Hashed, NewFiles: rep.NewFiles, Errors: len(rep.Errors)})
	return rep, err
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path) // read-only: originals are never modified
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
