package split

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/wildcar/librarian/internal/catalog"
)

// TestCalibrateRules measures every rule against hand-labelled boundaries.
// It runs only when both are given:
//
//	LIBRARIAN_SPLIT_CATALOG=<account>/catalog.sqlite
//	LIBRARIAN_SPLIT_REFERENCE=<split_reference.json>
//
// The reference format is the one of tools/ocrbench: {"<sha256[:12]>":
// {"starts": [...], "unsure": [...]}}. A rule may be strong only if it is
// (close to) always right here.
func TestCalibrateRules(t *testing.T) {
	catPath, refPath := os.Getenv("LIBRARIAN_SPLIT_CATALOG"), os.Getenv("LIBRARIAN_SPLIT_REFERENCE")
	if catPath == "" || refPath == "" {
		t.Skip("set LIBRARIAN_SPLIT_CATALOG and LIBRARIAN_SPLIT_REFERENCE to calibrate")
	}
	raw, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatal(err)
	}
	var ref map[string]json.RawMessage
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(catPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	docs, err := c.Documents(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	shas := map[string]bool{}
	for _, d := range docs {
		shas[d.SHA256] = true
	}
	type tally struct{ right, wrong int }
	byReason := map[string]*tally{}
	for sha := range shas {
		var spec struct {
			Starts []int `json:"starts"`
			Unsure []int `json:"unsure"`
		}
		r, ok := ref[sha[:12]]
		if !ok {
			continue
		}
		if err := json.Unmarshal(r, &spec); err != nil {
			t.Fatal(err)
		}
		isStart, unsure := map[int]bool{}, map[int]bool{}
		for _, s := range spec.Starts {
			isStart[s] = true
		}
		for _, u := range spec.Unsure {
			unsure[u] = true
		}
		ps, err := c.Pages(ctx, sha)
		if err != nil {
			t.Fatal(err)
		}
		for k := 1; k < len(ps); k++ {
			if unsure[k] {
				continue
			}
			v := Boundary(ps[k-1].Text, ps[k].Text)
			key := fmt.Sprintf("%-40s strong=%v", v.Reason, v.Strong)
			if byReason[key] == nil {
				byReason[key] = &tally{}
			}
			if v.New == isStart[k+1] {
				byReason[key].right++
			} else {
				byReason[key].wrong++
			}
		}
	}
	keys := make([]string, 0, len(byReason))
	for k := range byReason {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		x := byReason[k]
		fmt.Fprintf(&b, "%s  right %3d  wrong %3d\n", k, x.right, x.wrong)
	}
	t.Log("\n" + b.String())
}
