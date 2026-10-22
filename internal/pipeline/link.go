package pipeline

import (
	"context"
	"fmt"
	"slices"

	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/cloud"
	"github.com/wildcar/librarian/internal/index"
	"github.com/wildcar/librarian/internal/intake"
)

// Link is one intake of a public cloud link with the batch it made.
type Link struct {
	Service string
	Files   int      // in the share
	New     int      // of them, originals new to the account
	Skipped []string // files not kept, with the reason
	Batch   catalog.Batch
	Index   *index.Report
}

// IngestLink downloads every file of a public link into the account's store
// (ADR-0007: links die, so the originals are kept) and makes their batch as
// a folder intake does. progress hears how many files are done.
func IngestLink(ctx context.Context, c *catalog.Catalog, root, link string, maxFile int64, l Labels, progress func(done, total int)) (*Link, error) {
	share, err := cloud.Parse(link)
	if err != nil {
		return nil, err
	}
	files, err := share.Files(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", share.Service(), err)
	}
	r := &Link{Service: share.Service(), Files: len(files)}
	var seen, fresh []string
	for i, f := range files {
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		if progress != nil {
			progress(i, len(files))
		}
		if f.Size > maxFile {
			r.Skipped = append(r.Skipped, fmt.Sprintf("%s: больше %d МБ", f.Path, maxFile>>20))
			continue
		}
		body, err := share.Open(ctx, f)
		if err != nil {
			r.Skipped = append(r.Skipped, fmt.Sprintf("%s: %v", f.Path, err))
			continue
		}
		st, err := intake.Store(ctx, c, root, f.Path, body, maxFile)
		body.Close()
		if err != nil {
			r.Skipped = append(r.Skipped, err.Error())
			continue
		}
		if !slices.Contains(seen, st.SHA256) {
			seen = append(seen, st.SHA256)
			if st.New {
				fresh, r.New = append(fresh, st.SHA256), r.New+1
			}
		}
	}
	r.Batch, r.Index, err = makeBatch(ctx, c, "link", link, l, seen, fresh)
	return r, err
}
