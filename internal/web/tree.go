package web

import (
	"sort"
	"strings"

	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/titling"
)

// Levels of the virtual tree (ADR-0007): a person, a year, or any
// characteristic key.
const (
	LevelPerson = "person"
	LevelYear   = "year"
)

// Node is a branch of the tree; documents hang on the last level.
type Node struct {
	Label string
	None  bool // the «нет …» branch of its level
	Count int  // documents under it, each once
	Nodes []*Node
	Docs  []catalog.ShelfDoc
}

// Build groups documents by the levels in order. A document with several
// values on a level (two persons, two tags) appears under each; one without a
// value goes to the level's «нет …» branch, so nothing drops out of the tree.
func Build(docs []catalog.ShelfDoc, levels []string, labels map[string]string) *Node {
	root := &Node{Count: len(docs)}
	if len(levels) == 0 {
		root.Docs = docs
		return root
	}
	level := levels[0]
	groups := map[string][]catalog.ShelfDoc{}
	for _, d := range docs {
		vs := values(d, level)
		if len(vs) == 0 {
			vs = []string{""}
		}
		for _, v := range vs {
			groups[v] = append(groups[v], d)
		}
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		switch {
		case a == "" || b == "":
			return b == "" && a != "" // «нет …» last
		case level == LevelYear:
			return a > b // newest first
		case level == LevelPerson:
			return personLabel(a, labels) < personLabel(b, labels)
		}
		return strings.ToLower(a) < strings.ToLower(b)
	})
	for _, k := range keys {
		n := Build(groups[k], levels[1:], labels)
		n.Label, n.None = k, k == ""
		switch {
		case n.None:
			n.Label = noneLabel(level)
		case level == LevelPerson:
			n.Label = personLabel(k, labels)
		}
		root.Nodes = append(root.Nodes, n)
	}
	return root
}

func values(d catalog.ShelfDoc, level string) []string {
	switch level {
	case LevelPerson:
		return d.Persons
	case LevelYear:
		if y := Year(d); y != "" {
			return []string{y}
		}
		return nil
	}
	return d.Attrs[level]
}

// Year is the document's year, also from a date without a day; "" without one.
func Year(d catalog.ShelfDoc) string {
	for _, s := range []string{d.Date, d.Partial} {
		if len(s) >= 4 && s[0] != '-' {
			return s[:4]
		}
	}
	return ""
}

func personLabel(code string, labels map[string]string) string {
	if l := labels[code]; l != "" {
		return l + " (" + code + ")"
	}
	return code
}

func noneLabel(level string) string {
	switch level {
	case LevelPerson:
		return "нет человека"
	case LevelYear:
		return "нет даты"
	}
	return "нет «" + level + "»"
}

// Title stands in for a document's title until the model has made one: the
// first sentence of the summary, which names the document (ADR-0005),
// without its leading «Это».
func Title(summary string) string {
	s := strings.TrimSpace(summary)
	if rest, ok := strings.CutPrefix(s, "Это "); ok {
		s = titling.Clean(rest)
	}
	for i := 0; i+1 < len(s); i++ {
		if (s[i] == '.' || s[i] == '\n') && (s[i+1] == ' ' || s[i+1] == '\n') {
			return s[:i]
		}
	}
	return strings.TrimSuffix(s, ".")
}
