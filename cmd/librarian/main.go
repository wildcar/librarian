// Command librarian catalogues documents for isolated accounts (ADR-0004).
//
//	librarian [--home DIR] account create <id>
//	librarian [--home DIR] ingest  --account <id> [--workers N] <folder>
//	librarian [--home DIR] dupes   --account <id>
//	librarian [--home DIR] stats   --account <id>
//	librarian [--home DIR] extract --account <id> [--limit N] [--workers N] [--model ID] [--dry-run] [--no-check]
//	librarian [--home DIR] verify  --account <id> [--limit N] [--workers N]
//	librarian [--home DIR] split   --account <id> [--limit N] [--model ID]
//	librarian [--home DIR] docs    --account <id> [<sha256 prefix>]
//	librarian [--home DIR] text    --account <id> <sha256 prefix>
//	librarian version
//
// The data root is --home or LIBRARIAN_HOME; every account lives in its own
// directory under it. extract reads ROUTER_URL and ROUTER_TOKEN for the
// model router, and LIBRARIAN_POPPLER_DIR when poppler is not on PATH;
// extract and verify read LIBRARIAN_TESSERACT and LIBRARIAN_TESSDATA for the
// tesseract second reading.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/extract"
	"github.com/wildcar/librarian/internal/intake"
	"github.com/wildcar/librarian/internal/pages"
	"github.com/wildcar/librarian/internal/report"
	"github.com/wildcar/librarian/internal/router"
	"github.com/wildcar/librarian/internal/split"
)

// version is overridden at build time: -ldflags "-X main.version=..."
var version = "dev"

const usage = `usage:
  librarian [--home DIR] account create <id>
  librarian [--home DIR] ingest --account <id> [--workers N] <folder>
  librarian [--home DIR] dupes  --account <id>
  librarian [--home DIR] stats  --account <id>
  librarian [--home DIR] extract --account <id> [--limit N] [--workers N] [--model ID] [--dry-run] [--no-check]
  librarian [--home DIR] verify --account <id> [--limit N] [--workers N]
  librarian [--home DIR] split  --account <id> [--limit N] [--model ID]
  librarian [--home DIR] docs   --account <id> [<sha256 prefix>]
  librarian [--home DIR] text   --account <id> <sha256 prefix>
  librarian version

The data root is --home or the LIBRARIAN_HOME environment variable.`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "librarian:", err)
		var u usageError
		if errors.As(err, &u) {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		os.Exit(1)
	}
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func run(ctx context.Context, args []string, out io.Writer) error {
	global := flag.NewFlagSet("librarian", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	home := global.String("home", os.Getenv("LIBRARIAN_HOME"), "data root")
	if err := global.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	args = global.Args()
	if len(args) == 0 {
		return usageError{"no command"}
	}
	cmd, rest := args[0], args[1:]
	if cmd == "version" {
		fmt.Fprintln(out, "librarian", version)
		return nil
	}
	if *home == "" {
		return usageError{"data root not set: pass --home or set LIBRARIAN_HOME"}
	}
	switch cmd {
	case "account":
		if len(rest) != 2 || rest[0] != "create" {
			return usageError{"expected: account create <id>"}
		}
		a, err := account.Create(*home, rest[1])
		if err != nil {
			return err
		}
		c, err := catalog.Open(a.CatalogPath())
		if err != nil {
			return err
		}
		defer c.Close()
		fmt.Fprintf(out, "Аккаунт %s создан: %s\n", a.ID, a.Root())
		return nil
	case "ingest", "dupes", "stats", "extract", "verify", "text", "split", "docs":
		fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.String("account", "", "account id")
		workers := fs.Int("workers", 4, "parallel workers (hashing, OCR calls)")
		limit := fs.Int("limit", 0, "extract: originals per run, 0 = all pending")
		model := fs.String("model", extract.DefaultModel, "extract: OCR model id")
		dryRun := fs.Bool("dry-run", false, "extract: count pages and estimate cost only")
		noCheck := fs.Bool("no-check", false, "extract: skip the tesseract second reading")
		if err := fs.Parse(rest); err != nil {
			return usageError{err.Error()}
		}
		if *id == "" {
			return usageError{"--account is required"}
		}
		a, err := account.Open(*home, *id)
		if err != nil {
			return err
		}
		c, err := catalog.Open(a.CatalogPath())
		if err != nil {
			return err
		}
		defer c.Close()
		switch cmd {
		case "ingest":
			if fs.NArg() != 1 {
				return usageError{"expected one folder"}
			}
			rep, err := intake.Folder(ctx, c, fs.Arg(0), *workers)
			if err != nil {
				return err
			}
			report.Intake(out, rep)
		case "dupes":
			d, err := c.Duplicates(ctx)
			if err != nil {
				return err
			}
			report.Duplicates(out, d)
		case "stats":
			s, err := c.Stats(ctx)
			if err != nil {
				return err
			}
			mm, err := c.MIMEMismatches(ctx)
			if err != nil {
				return err
			}
			report.Stats(out, s, mm)
			x, err := c.ExtractionSummary(ctx)
			if err != nil {
				return err
			}
			report.Extraction(out, x)
			fl, err := c.FlaggedPages(ctx)
			if err != nil {
				return err
			}
			report.FlaggedPages(out, fl)
			bs, nd, notes, err := c.SplitSummary(ctx)
			if err != nil {
				return err
			}
			report.SplitState(out, bs, nd, notes)
		case "split":
			rc, err := router.Dial(ctx, os.Getenv("ROUTER_URL"), os.Getenv("ROUTER_TOKEN"))
			if err != nil {
				return err
			}
			defer rc.Close()
			model := *model
			if model == extract.DefaultModel {
				model = split.DefaultModel
			}
			st := &split.Stage{Catalog: c, Model: rc}
			rep, err := st.Run(ctx, split.Options{Account: a.ID, Model: model, Limit: *limit})
			if rep != nil {
				report.SplitRun(out, rep)
			}
			return err
		case "docs":
			sha := ""
			if fs.NArg() == 1 {
				if sha, err = c.ResolveSHA(ctx, fs.Arg(0)); err != nil {
					return err
				}
			}
			d, err := c.Documents(ctx, sha)
			if err != nil {
				return err
			}
			report.Documents(out, d)
		case "verify":
			st, err := stage(ctx, c, true)
			if err != nil {
				return err
			}
			rep, err := st.Verify(ctx, extract.Options{Account: a.ID, Workers: *workers, Limit: *limit})
			if rep != nil {
				report.VerifyRun(out, rep)
				if fl, ferr := c.FlaggedPages(ctx); ferr == nil {
					report.FlaggedPages(out, fl)
				}
			}
			return err
		case "extract":
			st, err := stage(ctx, c, !*noCheck && !*dryRun)
			if err != nil {
				return err
			}
			if !*dryRun {
				rc, err := router.Dial(ctx, os.Getenv("ROUTER_URL"), os.Getenv("ROUTER_TOKEN"))
				if err != nil {
					return err
				}
				defer rc.Close()
				st.OCR = rc
			}
			rep, err := st.Run(ctx, extract.Options{
				Account: a.ID, Model: *model, Workers: *workers, Limit: *limit, DryRun: *dryRun,
			})
			if rep != nil {
				report.ExtractRun(out, rep, *dryRun)
			}
			return err
		case "text":
			if fs.NArg() != 1 {
				return usageError{"expected a sha256 prefix"}
			}
			sha, err := c.ResolveSHA(ctx, fs.Arg(0))
			if err != nil {
				return err
			}
			ps, err := c.Pages(ctx, sha)
			if err != nil {
				return err
			}
			report.PageText(out, sha, ps)
		}
		return nil
	}
	return usageError{fmt.Sprintf("unknown command %q", cmd)}
}

// stage builds the extraction stage from the environment: poppler always,
// tesseract when the second reading is wanted.
func stage(ctx context.Context, c *catalog.Catalog, withCheck bool) (*extract.Stage, error) {
	pop := pages.Poppler{Dir: os.Getenv("LIBRARIAN_POPPLER_DIR")}
	if err := pop.Check(); err != nil {
		return nil, err
	}
	st := &extract.Stage{Catalog: c, Poppler: pop}
	if withCheck {
		t := pages.Tesseract{Exe: os.Getenv("LIBRARIAN_TESSERACT"), Tessdata: os.Getenv("LIBRARIAN_TESSDATA")}
		if err := t.Check(ctx); err != nil {
			return nil, fmt.Errorf("%w (or run extract with --no-check)", err)
		}
		st.Checker = t
	}
	return st, nil
}
