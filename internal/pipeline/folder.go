package pipeline

import (
	"context"
	"time"

	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/index"
	"github.com/wildcar/librarian/internal/intake"
)

// Labels are what the owner says about a batch (ADR-0006): whose documents
// they are, maybe a person to create first, and characteristics.
type Labels struct {
	Persons []string
	New     *catalog.Person // created before the batch; its code joins Persons
	Attrs   []catalog.Attr
	Label   string
	Actor   string // the assistant that sent the intake over MCP
	Reason  string // the user's words it passed on
}

func (l Labels) labelled() bool { return len(l.Persons) > 0 || l.New != nil || len(l.Attrs) > 0 }

// Folder is one folder intake with the batch it made.
type Folder struct {
	Intake *intake.Report
	Batch  catalog.Batch // ID 0 when the run made no batch
	Index  *index.Report // when labels were given
}

// IngestFolder records a folder's files and makes their batch: the new
// originals, or, when the owner labelled the folder, every original seen in
// it, since the owner labelled the folder as a whole. A run that brings
// nothing new and labels nothing makes no batch.
func IngestFolder(ctx context.Context, c *catalog.Catalog, path string, workers int, l Labels) (*Folder, error) {
	rep, err := intake.Folder(ctx, c, path, workers)
	if err != nil {
		return nil, err
	}
	f := &Folder{Intake: rep}
	f.Batch, f.Index, err = makeBatch(ctx, c, "folder", intake.URI(rep.Root), l, rep.Seen, rep.New)
	return f, err
}

// makeBatch makes the batch of one intake: the new originals, or, when the
// owner labelled the intake, every original seen in it, since the owner
// labelled it as a whole. Nothing new and no labels make no batch (ID 0).
func makeBatch(ctx context.Context, c *catalog.Catalog, kind, root string, l Labels, seen, fresh []string) (catalog.Batch, *index.Report, error) {
	originals := fresh
	if l.labelled() {
		originals = seen
	}
	if len(originals) == 0 {
		return catalog.Batch{}, nil, nil
	}
	now := time.Now()
	persons := append([]string{}, l.Persons...)
	if l.New != nil {
		if err := c.AddPerson(ctx, *l.New, now); err != nil {
			return catalog.Batch{}, nil, err
		}
		persons = append(persons, l.New.Code)
	}
	b := catalog.Batch{Kind: kind, Root: root, Label: l.Label, Persons: persons, Attrs: l.Attrs,
		Actor: l.Actor, Reason: l.Reason, Originals: len(originals)}
	var err error
	if b.ID, err = c.CreateBatch(ctx, b, originals, now); err != nil {
		return catalog.Batch{}, nil, err
	}
	var rep *index.Report
	if l.labelled() {
		rep, err = index.Run(ctx, c, now)
	}
	return b, rep, err
}
