package titling

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"Рекомендации уролога по лечению":               "Рекомендации уролога по лечению",
		"«Протокол гастроскопии».\n":                    "Протокол гастроскопии",
		"Название: суточное мониторирование ЭКГ":        "Суточное мониторирование ЭКГ",
		"Это выписка по счёту.":                         "Выписка по счёту",
		"**Дополнительное соглашение к договору**\nили": "Дополнительное соглашение к договору",
		"  ": "",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
	long := Clean(strings.Repeat("слово ", 30))
	if utf8.RuneCountInString(long) > MaxRunes || strings.HasSuffix(long, " ") || !strings.HasPrefix(long, "Слово") {
		t.Errorf("long: %q", long)
	}
}
