// Command librarian catalogues documents for isolated accounts (ADR-0004).
//
//	librarian [--home DIR] account create [--email E] <id>
//	librarian [--home DIR] account email <id> <e-mail> | unbind <e-mail> | list
//	librarian [--home DIR] login-link <e-mail>
//	librarian [--home DIR] ingest  --account <id> [--workers N] [batch labels] [--batch-label L] <folder>
//	librarian [--home DIR] batch   list --account <id>
//	librarian [--home DIR] batch   set  --account <id> [batch labels] [--no-person] [--unset KEY[=VALUE]]... <batch id>
//	librarian [--home DIR] doc     show --account <id> <document id>
//	librarian [--home DIR] doc     set  --account <id> [--person CODE]... [--unlink CODE]... [--no-person]
//	                                [--attr KEY=VALUE]... [--unset KEY[=VALUE]]... [--date D | --no-date | --clear-date]
//	                                [--reason TEXT] <document id>
//	librarian [--home DIR] doc     confirm|revert --account <id> <document id>
//	librarian [--home DIR] dupes   --account <id>
//	librarian [--home DIR] stats   --account <id>
//	librarian [--home DIR] extract --account <id> [--limit N] [--workers N] [--model ID] [--dry-run] [--no-check]
//	librarian [--home DIR] verify  --account <id> [--limit N] [--workers N]
//	librarian [--home DIR] split   --account <id> [--limit N] [--model ID]
//	librarian [--home DIR] docs    --account <id> [<sha256 prefix>]
//	librarian [--home DIR] digest  --account <id> [--limit N] [--workers N] [--model ID]
//	librarian [--home DIR] date    --account <id> [--limit N] [--workers N] [--model ID]
//	librarian [--home DIR] show    --account <id> [--layer summary|essential|verbatim] <document id>
//	librarian [--home DIR] person  add|list --account <id> [--code --name --label --alias --birth]
//	librarian [--home DIR] index   --account <id>
//	librarian [--home DIR] token   create --account <id>
//	librarian [--home DIR] serve   [--addr 127.0.0.1:8090]
//	librarian [--home DIR] text    --account <id> <sha256 prefix>
//	librarian version
//
// Batch labels (ADR-0006): --person CODE (repeatable; with --name, --label,
// --birth, --alias a new person is created) and --attr KEY=VALUE (repeatable).
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
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/dating"
	"github.com/wildcar/librarian/internal/extract"
	"github.com/wildcar/librarian/internal/index"
	"github.com/wildcar/librarian/internal/intake"
	"github.com/wildcar/librarian/internal/layers"
	"github.com/wildcar/librarian/internal/mail"
	"github.com/wildcar/librarian/internal/mcpserver"
	"github.com/wildcar/librarian/internal/pages"
	"github.com/wildcar/librarian/internal/report"
	"github.com/wildcar/librarian/internal/router"
	"github.com/wildcar/librarian/internal/split"
	"github.com/wildcar/librarian/internal/users"
	"github.com/wildcar/librarian/internal/web"
)

// version is overridden at build time: -ldflags "-X main.version=..."
var version = "dev"

const usage = `usage:
  librarian [--home DIR] account create [--email E] <id>
  librarian [--home DIR] account email <id> <e-mail> | unbind <e-mail> | list
  librarian [--home DIR] login-link <e-mail>
  librarian [--home DIR] ingest --account <id> [--workers N] [labels] [--batch-label L] <folder>
  librarian [--home DIR] batch list --account <id>
  librarian [--home DIR] batch set  --account <id> [labels] [--no-person] [--unset KEY[=VALUE]]... <batch id>
  librarian [--home DIR] doc show --account <id> <document id>
  librarian [--home DIR] doc set  --account <id> [--person CODE]... [--unlink CODE]... [--no-person]
        [--attr KEY=VALUE]... [--unset KEY[=VALUE]]... [--date YYYY-MM-DD|YYYY-MM|YYYY|--MM-DD | --no-date | --clear-date]
        [--reason TEXT] <document id>
  librarian [--home DIR] doc confirm|revert --account <id> <document id>
  librarian [--home DIR] dupes  --account <id>
  librarian [--home DIR] stats  --account <id>
  librarian [--home DIR] extract --account <id> [--limit N] [--workers N] [--model ID] [--dry-run] [--no-check]
  librarian [--home DIR] verify --account <id> [--limit N] [--workers N]
  librarian [--home DIR] split  --account <id> [--limit N] [--model ID]
  librarian [--home DIR] docs   --account <id> [<sha256 prefix>]
  librarian [--home DIR] digest --account <id> [--limit N] [--workers N] [--model ID]
  librarian [--home DIR] date   --account <id> [--limit N] [--workers N] [--model ID]
  librarian [--home DIR] show   --account <id> [--layer summary|essential|verbatim] <document id>
  librarian [--home DIR] person add  --account <id> --code C --name "..." [--label L] [--alias A]... [--birth YYYY-MM-DD]
  librarian [--home DIR] person list --account <id>
  librarian [--home DIR] index  --account <id>
  librarian [--home DIR] token create --account <id>
  librarian [--home DIR] serve  [--addr 127.0.0.1:8090]
  librarian [--home DIR] text   --account <id> <sha256 prefix>
  librarian version

doc set refines one document; it outranks batches, rules and models and index keeps it:
        --unlink and --unset also stop a batch or rule from bringing the person or value back.
doc confirm makes an assistant's refinements (MCP update_document) the owner's; doc revert drops them.

labels: --person CODE (repeatable) — whose documents these are; with --name "..." [--label L]
        [--birth YYYY-MM-DD] [--alias A]... a new person is created under CODE;
        --attr KEY=VALUE (repeatable) — characteristics every document of the batch gets.

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
		return accountCmd(ctx, *home, rest, out)
	case "login-link":
		if len(rest) != 1 {
			return usageError{"expected: login-link <e-mail>"}
		}
		return loginLink(ctx, *home, rest[0], out)
	case "serve":
		fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		addr := fs.String("addr", "127.0.0.1:8090", "listen address")
		if err := fs.Parse(rest); err != nil {
			return usageError{err.Error()}
		}
		return serve(ctx, *home, *addr, out)
	case "token", "person", "index", "batch", "doc":
		return accountAdmin(ctx, *home, cmd, rest, out)
	case "ingest", "dupes", "stats", "extract", "verify", "text", "split", "docs", "digest", "date", "show":
		fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		id := fs.String("account", "", "account id")
		workers := fs.Int("workers", 4, "parallel workers (hashing, OCR calls)")
		limit := fs.Int("limit", 0, "extract: originals per run, 0 = all pending")
		model := fs.String("model", extract.DefaultModel, "extract: OCR model id")
		dryRun := fs.Bool("dry-run", false, "extract: count pages and estimate cost only")
		noCheck := fs.Bool("no-check", false, "extract: skip the tesseract second reading")
		layer := fs.String("layer", "summary", "show: summary | essential | verbatim")
		batchLabel := fs.String("batch-label", "", "ingest: a name for the batch")
		var labels batchFlags
		labels.register(fs)
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
			attrs, err := labels.check(ctx, c)
			if err != nil {
				return err
			}
			rep, err := intake.Folder(ctx, c, fs.Arg(0), *workers)
			if err != nil {
				return err
			}
			report.Intake(out, rep)
			// The new originals make the batch; a labelled run takes every
			// original it saw: the owner labelled the folder as a whole.
			labelled := len(labels.persons) > 0 || len(attrs) > 0
			originals := rep.New
			if labelled {
				originals = rep.Seen
			}
			if len(originals) == 0 {
				return nil
			}
			if err := labels.createPerson(ctx, c, out); err != nil {
				return err
			}
			b := catalog.Batch{Kind: "folder", Root: intake.URI(rep.Root), Label: *batchLabel, Persons: labels.persons, Attrs: attrs}
			if b.ID, err = c.CreateBatch(ctx, b, originals, time.Now()); err != nil {
				return err
			}
			b.Originals = len(originals)
			report.BatchCreated(out, b)
			if labelled {
				return reindex(ctx, c, out)
			}
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
			tb, tc, tn, err := c.TextsSummary(ctx)
			if err != nil {
				return err
			}
			report.TextsState(out, tb, tc, tn)
		case "digest":
			rc, err := router.Dial(ctx, os.Getenv("ROUTER_URL"), os.Getenv("ROUTER_TOKEN"))
			if err != nil {
				return err
			}
			defer rc.Close()
			m := *model
			if m == extract.DefaultModel {
				m = layers.DefaultModel
			}
			st := &layers.Stage{Catalog: c, Model: rc}
			rep, err := st.Run(ctx, layers.Options{Account: a.ID, Model: m, Workers: *workers, Limit: *limit})
			if rep != nil {
				report.DigestRun(out, rep)
			}
			return err
		case "date":
			rc, err := router.Dial(ctx, os.Getenv("ROUTER_URL"), os.Getenv("ROUTER_TOKEN"))
			if err != nil {
				return err
			}
			defer rc.Close()
			m := *model
			if m == extract.DefaultModel {
				m = dating.DefaultModel
			}
			st := &dating.Stage{Catalog: c, Model: rc}
			rep, err := st.Run(ctx, dating.Options{Account: a.ID, Model: m, Workers: *workers, Limit: *limit})
			if rep != nil {
				report.DateRun(out, rep)
			}
			return err
		case "show":
			if fs.NArg() != 1 {
				return usageError{"expected a document id"}
			}
			d, t, err := c.GetDocument(ctx, fs.Arg(0))
			if err != nil {
				return err
			}
			switch *layer {
			case "summary", "essential":
				report.ShowLayer(out, d, t, *layer)
			case "verbatim":
				ps, err := c.Pages(ctx, d.SHA256)
				if err != nil {
					return err
				}
				var in []catalog.Page
				for _, p := range ps {
					if p.Page >= d.FirstPage && p.Page <= d.LastPage {
						in = append(in, p)
					}
				}
				report.PageText(out, d.SHA256, in)
			default:
				return usageError{"--layer must be summary, essential or verbatim"}
			}
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

// serve runs the MCP endpoint and the web interface until the context ends.
// The web needs LIBRARIAN_PUBLIC_URL (the address people open) and mail
// settings: SMTP_ADDR (host:port), MAIL_FROM, and SMTP_USER / SMTP_PASSWORD
// for a relay that wants a login. Without SMTP_ADDR letters go to the log:
// for development only, they carry login links.
func serve(ctx context.Context, home, addr string, out io.Writer) error {
	s := mcpserver.New(home)
	defer s.Close()
	reg, err := users.Open(home)
	if err != nil {
		return err
	}
	defer reg.Close()
	public := os.Getenv("LIBRARIAN_PUBLIC_URL")
	if public == "" {
		public = "http://" + addr
	}
	var sender mail.Sender = mail.Log{W: out}
	if a := os.Getenv("SMTP_ADDR"); a != "" {
		from := os.Getenv("MAIL_FROM")
		if from == "" {
			return fmt.Errorf("SMTP_ADDR is set but MAIL_FROM is not")
		}
		sender = mail.SMTP{Addr: a, User: os.Getenv("SMTP_USER"), Password: os.Getenv("SMTP_PASSWORD"), From: from}
	} else {
		fmt.Fprintln(out, "SMTP_ADDR не задан: письма для входа пишутся в журнал (только для разработки)")
	}
	w, err := web.New(web.Config{Registry: reg, Mail: sender, Public: public, Catalog: s.Catalog, Log: out,
		HasStaticToken: (*account.Account).HasToken})
	if err != nil {
		return err
	}
	s.SetWeb(w.Handler())
	s.SetOAuth(func(ctx context.Context, token string) (string, string, time.Time, error) {
		a, err := reg.VerifyAccess(ctx, token, time.Now())
		return a.Account, a.Assistant, a.Expires, err
	}, w.ResourceMetadataURL())
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	fmt.Fprintf(out, "MCP: %s/mcp (OAuth по письму или Bearer <токен аккаунта>)\nВеб: %s\n", public, public)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// accountCmd creates accounts and binds the e-mails that log into them.
func accountCmd(ctx context.Context, home string, rest []string, out io.Writer) error {
	if len(rest) == 0 {
		return usageError{"expected: account create | email | unbind | list"}
	}
	fs := flag.NewFlagSet("account", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	email := fs.String("email", "", "create: the e-mail that logs into the account")
	if err := fs.Parse(rest[1:]); err != nil {
		return usageError{err.Error()}
	}
	now := time.Now()
	switch rest[0] {
	case "create":
		if fs.NArg() != 1 {
			return usageError{"expected: account create [--email E] <id>"}
		}
		a, err := account.Create(home, fs.Arg(0))
		if err != nil {
			return err
		}
		c, err := catalog.Open(a.CatalogPath())
		if err != nil {
			return err
		}
		defer c.Close()
		fmt.Fprintf(out, "Аккаунт %s создан: %s\n", a.ID, a.Root())
		if *email == "" {
			return nil
		}
		return bindEmail(ctx, home, a.ID, *email, now, out)
	case "email":
		if fs.NArg() != 2 {
			return usageError{"expected: account email <id> <e-mail>"}
		}
		return bindEmail(ctx, home, fs.Arg(0), fs.Arg(1), now, out)
	case "unbind":
		if fs.NArg() != 1 {
			return usageError{"expected: account unbind <e-mail>"}
		}
		reg, err := users.Open(home)
		if err != nil {
			return err
		}
		defer reg.Close()
		if err := reg.RemoveEmail(ctx, fs.Arg(0)); err != nil {
			return err
		}
		fmt.Fprintf(out, "Почта %s больше не входит ни в какой аккаунт, её сессии закрыты\n", fs.Arg(0))
		return nil
	case "list":
		reg, err := users.Open(home)
		if err != nil {
			return err
		}
		defer reg.Close()
		us, err := reg.Users(ctx)
		if err != nil {
			return err
		}
		if len(us) == 0 {
			fmt.Fprintln(out, "Ни одна почта не привязана: account email <id> <e-mail>")
		}
		for _, u := range us {
			fmt.Fprintf(out, "%-20s %s\n", u.Account, u.Email)
		}
		return nil
	}
	return usageError{fmt.Sprintf("unknown account command %q", rest[0])}
}

func bindEmail(ctx context.Context, home, id, email string, now time.Time, out io.Writer) error {
	reg, err := users.Open(home)
	if err != nil {
		return err
	}
	defer reg.Close()
	if err := reg.SetEmail(ctx, email, id, now); err != nil {
		return err
	}
	fmt.Fprintf(out, "Почта %s входит в аккаунт %s\n", email, id)
	return nil
}

// loginLink prints a one-time login link for the administrator, for when a
// letter does not arrive: whoever opens it and presses the button is logged in.
func loginLink(ctx context.Context, home, email string, out io.Writer) error {
	reg, err := users.Open(home)
	if err != nil {
		return err
	}
	defer reg.Close()
	l, err := reg.StartLogin(ctx, email, users.Request{Purpose: users.PurposeDirect, Agent: "командная строка"}, time.Now())
	if err != nil {
		return err
	}
	public := os.Getenv("LIBRARIAN_PUBLIC_URL")
	if public == "" {
		public = "http://127.0.0.1:8090"
	}
	fmt.Fprintf(out, "%s/login/confirm?t=%s\nДействует до %s, один раз.\n", strings.TrimRight(public, "/"), l.Link, l.Expires.Local().Format("15:04"))
	return nil
}

// stringList is a repeatable string flag.
// showDoc prints what the catalog knows about a document and where each fact
// came from.
func showDoc(ctx context.Context, c *catalog.Catalog, id string, out io.Writer) error {
	d, t, err := c.GetDocument(ctx, id)
	if err != nil {
		return err
	}
	var st report.DocState
	st.ID, st.Pages, st.Summary = d.ID, d.LastPage-d.FirstPage+1, t.Summary
	if st.Date, st.Partial, st.DateSource, err = c.DocDateOf(ctx, id); err != nil {
		return err
	}
	if st.Links, err = c.DocLinks(ctx, id); err != nil {
		return err
	}
	if st.Attrs, err = c.DocAttrSources(ctx, id); err != nil {
		return err
	}
	if st.Batches, err = c.DocBatches(ctx, id); err != nil {
		return err
	}
	if st.Rejected, err = c.DocRejected(ctx, id); err != nil {
		return err
	}
	if st.Review, err = c.DocReview(ctx, id); err != nil {
		return err
	}
	if st.Changes, err = c.DocChanges(ctx, id); err != nil {
		return err
	}
	report.Doc(out, st)
	return nil
}

// reindex reruns index and reports it; batch changes reach documents this way.
func reindex(ctx context.Context, c *catalog.Catalog, out io.Writer) error {
	rep, err := index.Run(ctx, c, time.Now())
	if err != nil {
		return err
	}
	un, err := c.Unlinked(ctx)
	if err != nil {
		return err
	}
	report.IndexRun(out, rep, un)
	return nil
}

// batchFlags are the owner's labels for a batch (ADR-0006): whose documents
// these are, an existing person or a new one, and their characteristics.
type batchFlags struct {
	persons, aliases, attrs stringList
	name, label, birth      string
}

func (f *batchFlags) register(fs *flag.FlagSet) {
	fs.Var(&f.persons, "person", "person code (repeatable)")
	fs.StringVar(&f.name, "name", "", "full name: creates the person given by --person/--code")
	fs.StringVar(&f.label, "label", "", "what the assistant sees, e.g. Папа")
	fs.StringVar(&f.birth, "birth", "", "birth date, YYYY-MM-DD")
	fs.Var(&f.aliases, "alias", "a spelling found in documents (repeatable)")
	fs.Var(&f.attrs, "attr", "characteristic KEY=VALUE (repeatable)")
}

// check validates the labels before anything is written: characteristics
// parse, named persons exist, a new person's code is free.
func (f *batchFlags) check(ctx context.Context, c *catalog.Catalog) ([]catalog.Attr, error) {
	var attrs []catalog.Attr
	for _, s := range f.attrs {
		a, err := catalog.ParseAttr(s)
		if err != nil {
			return nil, usageError{err.Error()}
		}
		attrs = append(attrs, a)
	}
	ps, err := c.Persons(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, p := range ps {
		known[p.Code] = true
	}
	if f.name != "" {
		if len(f.persons) != 1 {
			return nil, usageError{"--name creates one person: give exactly one --person CODE"}
		}
		if known[f.persons[0]] {
			return nil, fmt.Errorf("код %s уже занят; чтобы выбрать этого человека, не указывай --name", f.persons[0])
		}
		return attrs, nil
	}
	for _, code := range f.persons {
		if !known[code] {
			return nil, fmt.Errorf("человека с кодом %s нет: выбери из person list или создай, добавив --name", code)
		}
	}
	return attrs, nil
}

// createPerson adds the new person named by --name, if any.
func (f *batchFlags) createPerson(ctx context.Context, c *catalog.Catalog, out io.Writer) error {
	if f.name == "" {
		return nil
	}
	p := catalog.Person{Code: f.persons[0], Label: f.label, FullName: f.name, Aliases: f.aliases, BirthDate: f.birth}
	if err := c.AddPerson(ctx, p, time.Now()); err != nil {
		return err
	}
	fmt.Fprintf(out, "Добавлен %s (%s).\n", p.Code, p.FullName)
	return nil
}

type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ", ") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

// accountAdmin handles token, person, batch, doc and index.
func accountAdmin(ctx context.Context, home, cmd string, rest []string, out io.Writer) error {
	sub := ""
	if cmd != "index" {
		if len(rest) == 0 {
			return usageError{"expected: " + cmd + " <subcommand>"}
		}
		sub, rest = rest[0], rest[1:]
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	id := fs.String("account", "", "account id")
	code := fs.String("code", "", "person code")
	var labels batchFlags
	labels.register(fs)
	noPerson := fs.Bool("no-person", false, "batch set: the batch names no person; doc set: unlink every person")
	var unset, unlink stringList
	fs.Var(&unset, "unset", "batch/doc set: KEY or KEY=VALUE to remove (repeatable)")
	fs.Var(&unlink, "unlink", "doc set: a person this document is not about (repeatable)")
	date := fs.String("date", "", "doc set: YYYY-MM-DD, YYYY-MM, YYYY or --MM-DD")
	noDate := fs.Bool("no-date", false, "doc set: the document has no date")
	clearDate := fs.Bool("clear-date", false, "doc set: forget the manual date")
	why := fs.String("reason", "", "doc set: why, kept in the document's change log")
	if err := fs.Parse(rest); err != nil {
		return usageError{err.Error()}
	}
	if *id == "" {
		return usageError{"--account is required"}
	}
	name, label, birth, aliases := &labels.name, &labels.label, &labels.birth, labels.aliases
	a, err := account.Open(home, *id)
	if err != nil {
		return err
	}
	if cmd == "token" {
		if sub != "create" {
			return usageError{"expected: token create --account <id>"}
		}
		tok, err := a.NewToken()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Токен аккаунта %s (показывается один раз, прежний больше не действует):\n%s\n", a.ID, tok)
		return nil
	}
	c, err := catalog.Open(a.CatalogPath())
	if err != nil {
		return err
	}
	defer c.Close()
	switch {
	case cmd == "person" && sub == "add":
		p := catalog.Person{Code: *code, Label: *label, FullName: *name, Aliases: aliases, BirthDate: *birth}
		if err := c.AddPerson(ctx, p, time.Now()); err != nil {
			return err
		}
		fmt.Fprintf(out, "Добавлен %s (%s). Чтобы привязать документы, запусти index.\n", p.Code, *name)
	case cmd == "person" && sub == "list":
		ps, err := c.Persons(ctx)
		if err != nil {
			return err
		}
		report.Persons(out, ps)
	case cmd == "index":
		return reindex(ctx, c, out)
	case cmd == "batch" && sub == "list":
		bs, err := c.Batches(ctx)
		if err != nil {
			return err
		}
		report.Batches(out, bs)
	case cmd == "doc" && sub == "show":
		if fs.NArg() != 1 {
			return usageError{"expected a document id"}
		}
		return showDoc(ctx, c, fs.Arg(0), out)
	case cmd == "doc" && sub == "set":
		if fs.NArg() != 1 {
			return usageError{"expected a document id"}
		}
		doc := fs.Arg(0)
		dates := 0
		for _, b := range []bool{*date != "", *noDate, *clearDate} {
			if b {
				dates++
			}
		}
		if dates > 1 {
			return usageError{"--date, --no-date and --clear-date exclude each other"}
		}
		attrs, err := labels.check(ctx, c)
		if err != nil {
			return err
		}
		ch := catalog.DocChange{Link: labels.persons, Unlink: unlink, Attrs: attrs, ClearDate: *clearDate}
		if *noPerson {
			ls, err := c.DocLinks(ctx, doc)
			if err != nil {
				return err
			}
			for _, l := range ls {
				ch.Unlink = append(ch.Unlink, l.Code)
			}
		}
		for _, u := range unset {
			k, v, _ := strings.Cut(u, "=")
			ch.Unset = append(ch.Unset, catalog.Attr{Key: catalog.AttrKey(k), Value: strings.TrimSpace(v)})
		}
		switch {
		case *date != "":
			ch.Date = date
		case *noDate:
			none := ""
			ch.Date = &none
		}
		if err := labels.createPerson(ctx, c, out); err != nil {
			return err
		}
		if err := c.ChangeDocument(ctx, doc, ch, catalog.OriginOwner, *why, time.Now()); err != nil {
			return err
		}
		if err := index.Doc(ctx, c, doc, time.Now()); err != nil {
			return err
		}
		return showDoc(ctx, c, doc, out)
	case cmd == "doc" && (sub == "confirm" || sub == "revert"):
		if fs.NArg() != 1 {
			return usageError{"expected a document id"}
		}
		doc := fs.Arg(0)
		apply, verb := c.ConfirmAssistant, "подтверждено"
		if sub == "revert" {
			apply, verb = c.RevertAssistant, "откачено"
		}
		n, err := apply(ctx, doc)
		if err != nil {
			return err
		}
		if err := index.Doc(ctx, c, doc, time.Now()); err != nil {
			return err
		}
		fmt.Fprintf(out, "Правок ассистента %s: %d\n", verb, n)
		return showDoc(ctx, c, doc, out)
	case cmd == "batch" && sub == "set":
		if fs.NArg() != 1 {
			return usageError{"expected a batch id"}
		}
		bid, err := strconv.ParseInt(fs.Arg(0), 10, 64)
		if err != nil {
			return usageError{"batch id is a number: " + fs.Arg(0)}
		}
		if *noPerson && len(labels.persons) > 0 {
			return usageError{"--no-person and --person exclude each other"}
		}
		attrs, err := labels.check(ctx, c)
		if err != nil {
			return err
		}
		ch := catalog.BatchChange{Add: attrs}
		if len(labels.persons) > 0 || *noPerson {
			ps := []string(labels.persons)
			ch.Persons = &ps
		}
		for _, u := range unset {
			k, v, _ := strings.Cut(u, "=")
			ch.Remove = append(ch.Remove, catalog.Attr{Key: catalog.AttrKey(k), Value: strings.TrimSpace(v)})
		}
		if err := labels.createPerson(ctx, c, out); err != nil {
			return err
		}
		if err := c.ChangeBatch(ctx, bid, ch); err != nil {
			return err
		}
		bs, err := c.Batches(ctx)
		if err != nil {
			return err
		}
		for _, b := range bs {
			if b.ID == bid {
				report.Batches(out, []catalog.Batch{b})
			}
		}
		return reindex(ctx, c, out)
	default:
		return usageError{fmt.Sprintf("unknown %s subcommand %q", cmd, sub)}
	}
	return nil
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
