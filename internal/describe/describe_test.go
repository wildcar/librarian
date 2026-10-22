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
	d, err := Parse("```json\n{\"title\": \"Рекомендации уролога по лечению.\", \"вид\": \"Рекомендации\", \"специальность\": \"уролог\", \"специальность_догадка\": false, \"организация\": null}\n```")
	if err != nil || d.Title != "Рекомендации уролога по лечению" || len(d.Attrs) != 2 ||
		d.Attrs["вид"][0] != "рекомендации" || d.Attrs["специальность"][0] != "уролог" || len(d.Guessed) != 0 {
		t.Errorf("Parse: %+v %v", d, err)
	}
	d, _ = Parse(`{"title": "Суточное мониторирование ЭКГ", "вид": "ЭКГ", "специальность": "не указана", "специальность_догадка": true, "организация": "Частная клиника"}`)
	if d.Attrs["вид"][0] != "ЭКГ" || d.Attrs["организация"][0] != "частная клиника" || d.Attrs["специальность"] != nil || d.Guessed["специальность"] {
		t.Errorf("values: %+v (no value, so no guess)", d)
	}
	d, _ = Parse(`{"title": "Рекомендации по лечению", "вид": "рекомендации", "специальность": "эндокринолог", "специальность_догадка": true}`)
	if d.Attrs["специальность"][0] != "эндокринолог" || !d.Guessed["специальность"] {
		t.Errorf("guess: %+v", d)
	}
	for _, bad := range []string{"Рекомендации уролога", `{"вид": "счёт"}`} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) took it", bad)
		}
	}
}

func TestProperNames(t *testing.T) {
	d, _ := Parse(`{"title": "Справка", "вид": "справка", "специальность": "Терапевт", "организация": "Частная клиника «Здоровье»"}`)
	if d.Attrs["специальность"][0] != "терапевт" || d.Attrs["организация"] != nil {
		t.Errorf("attrs: %v", d.Attrs)
	}
	d, _ = Parse(`{"title": "Свидетельство о браке", "вид": "свидетельство", "организация": "орган ЗАГС"}`)
	if d.Attrs["организация"][0] != "орган ЗАГС" {
		t.Errorf("abbreviation: %v", d.Attrs)
	}
}
