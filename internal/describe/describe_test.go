package describe

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

func TestParse(t *testing.T) {
	title, attrs, err := Parse("```json\n{\"title\": \"Рекомендации уролога по лечению.\", \"вид\": \"Рекомендации\", \"специальность\": \"уролог\", \"организация\": null}\n```")
	if err != nil || title != "Рекомендации уролога по лечению" || len(attrs) != 2 ||
		attrs["вид"][0] != "рекомендации" || attrs["специальность"][0] != "уролог" {
		t.Errorf("Parse: %q %v %v", title, attrs, err)
	}
	_, attrs, _ = Parse(`{"title": "Суточное мониторирование ЭКГ", "вид": "ЭКГ", "специальность": "не указана", "организация": "Частная клиника"}`)
	if attrs["вид"][0] != "ЭКГ" || attrs["организация"][0] != "частная клиника" || attrs["специальность"] != nil {
		t.Errorf("values: %v", attrs)
	}
	for _, bad := range []string{"Рекомендации уролога", `{"вид": "счёт"}`} {
		if _, _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) took it", bad)
		}
	}
}

func TestProperNames(t *testing.T) {
	_, attrs, _ := Parse(`{"title": "Справка", "вид": "справка", "специальность": "Терапевт", "организация": "Частная клиника «Здоровье»"}`)
	if attrs["специальность"][0] != "терапевт" || attrs["организация"] != nil {
		t.Errorf("attrs: %v", attrs)
	}
	_, attrs, _ = Parse(`{"title": "Свидетельство о браке", "вид": "свидетельство", "организация": "орган ЗАГС"}`)
	if attrs["организация"][0] != "орган ЗАГС" {
		t.Errorf("abbreviation: %v", attrs)
	}
}
