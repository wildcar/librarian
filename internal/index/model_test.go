package index_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wildcar/librarian/internal/account"
	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/index"
)

// The model's characteristics rank below the owner's, an assistant's and a
// batch's, and one the owner removed is not read back in.
func TestModelAttrsRankLast(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	a, err := account.Create(t.TempDir(), "acc")
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(a.CatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sha := func(ch string) string { return strings.Repeat(ch, 64) }
	plain := doc(t, c, sha("1"), "Рекомендации")
	batched := doc(t, c, sha("2"), "Рекомендации")
	owned := doc(t, c, sha("3"), "Рекомендации")
	model := catalog.Description{Title: "Рекомендации уролога",
		Attrs: map[string][]string{"вид": {"рекомендации"}, "специальность": {"уролог"}}}
	for _, id := range []string{plain, batched, owned} {
		if err := c.SaveDescription(ctx, id, "Документ.", model); err != nil {
			t.Fatal(err)
		}
	}
	// The model guessed the specialty of one document.
	guessed := doc(t, c, sha("4"), "Рекомендации")
	model.Guessed = map[string]bool{"специальность": true}
	if err := c.SaveDescription(ctx, guessed, "Документ.", model); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateBatch(ctx, catalog.Batch{Kind: "upload", Attrs: []catalog.Attr{{Key: "специальность", Value: "андролог"}}},
		[]string{sha("2")}, now); err != nil {
		t.Fatal(err)
	}
	if err := c.ChangeDocument(ctx, owned, catalog.DocChange{Unset: []catalog.Attr{{Key: "вид"}}}, catalog.OriginOwner, "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Run(ctx, c, now); err != nil {
		t.Fatal(err)
	}
	attrs := func(id string) string {
		as, err := c.DocAttrSources(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, a := range as {
			out = append(out, a.Key+"="+a.Value+"@"+a.Source)
		}
		return strings.Join(out, " ")
	}
	for id, want := range map[string]string{
		plain:   "вид=рекомендации@model специальность=уролог@model",
		batched: "вид=рекомендации@model специальность=андролог@batch:1",
		owned:   "специальность=уролог@model",
		guessed: "вид=рекомендации@model специальность=уролог@model:guess",
	} {
		if got := attrs(id); got != want {
			t.Errorf("%s: %s, want %s", id, got, want)
		}
	}
	if g, err := c.DocGuesses(ctx, guessed); err != nil || strings.Join(g, " ") != "специальность=уролог" {
		t.Errorf("guesses: %v, %v", g, err)
	}
	// Indexing again changes nothing.
	if _, err := index.Run(ctx, c, now); err != nil {
		t.Fatal(err)
	}
	if got := attrs(plain); got != "вид=рекомендации@model специальность=уролог@model" {
		t.Errorf("again: %s", got)
	}
}
