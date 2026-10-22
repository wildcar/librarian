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
	originals := rep.New
	if l.labelled() {
		originals = rep.Seen
	}
	if len(originals) == 0 {
		return f, nil
	}
	now := time.Now()
	persons := append([]string{}, l.Persons...)
	if l.New != nil {
		if err := c.AddPerson(ctx, *l.New, now); err != nil {
			return f, err
		}
		persons = append(persons, l.New.Code)
	}
	f.Batch = catalog.Batch{Kind: "folder", Root: intake.URI(rep.Root), Label: l.Label, Persons: persons, Attrs: l.Attrs,
		Originals: len(originals)}
	if f.Batch.ID, err = c.CreateBatch(ctx, f.Batch, originals, now); err != nil {
		return f, err
	}
	if l.labelled() {
		f.Index, err = index.Run(ctx, c, now)
	}
	return f, err
}
