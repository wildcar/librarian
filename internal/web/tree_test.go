package web

import (
	"strings"
	"testing"

	"github.com/wildcar/librarian/internal/catalog"
)

func TestBuild(t *testing.T) {
	docs := []catalog.ShelfDoc{
		{ID: "a", Date: "2024-03-01", Persons: []string{"ЕБВ"}, Attrs: map[string][]string{"тема": {"сердце"}}},
		{ID: "b", Partial: "2019-05", Persons: []string{"ЕБВ", "ЕАИ"}},
		{ID: "c", Partial: "--09-12", Attrs: map[string][]string{"тема": {"почки", "сердце"}}},
		{ID: "d", Date: "2019-01-02", Persons: []string{"ЕАИ"}},
	}
	labels := map[string]string{"ЕБВ": "Папа", "ЕАИ": "Мама"}
	show := func(n *Node) string {
		var b strings.Builder
		var walk func(n *Node, depth int)
		walk = func(n *Node, depth int) {
			for _, c := range n.Nodes {
				b.WriteString(strings.Repeat(" ", depth) + c.Label + ":")
				for _, d := range c.Docs {
					b.WriteString(d.ID)
				}
				b.WriteString("\n")
				walk(c, depth+1)
			}
		}
		walk(n, 0)
		return b.String()
	}

	got := show(Build(docs, []string{LevelPerson, LevelYear}, labels))
	want := "Мама (ЕАИ):\n 2019:bd\nПапа (ЕБВ):\n 2024:a\n 2019:b\nнет человека:\n нет даты:c\n"
	if got != want {
		t.Errorf("person › year:\n%s\nwant:\n%s", got, want)
	}
	got = show(Build(docs, []string{"тема"}, labels))
	want = "почки:c\nсердце:ac\nнет «тема»:bd\n"
	if got != want {
		t.Errorf("тема:\n%s\nwant:\n%s", got, want)
	}
	if n := Build(docs, nil, labels); len(n.Docs) != 4 || n.Count != 4 {
		t.Errorf("no levels: %+v", n)
	}
}

func TestTitle(t *testing.T) {
	for in, want := range map[string]string{
		"Заключение кардиолога от 02.04.2022. Жалоб нет.": "Заключение кардиолога от 02.04.2022",
		"ЭКГ.":    "ЭКГ",
		"Справка": "Справка",
	} {
		if got := Title(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
