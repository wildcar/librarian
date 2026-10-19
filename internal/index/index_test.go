package index

import (
	"testing"
	"time"

	"github.com/wildcar/librarian/internal/catalog"
)

func TestDateFromSummary(t *testing.T) {
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	cases := map[string]string{
		"Это протокол УЗИ от 02.04.2022. Пациенту назначено 2 раза в день.":   "2022-04-02",
		"Выписной эпикриз от 8 декабря 2019 года. Поступил 04.12.2019.":       "2019-12-08",
		"Рекомендации, дата в тексте не указана. Повторный прием 12.08.2026.": "",
		"Детализация оплат от 02.06.26.":                                      "2026-06-02",
		"Дневник от 18.11.49.":    "1949-11-18",
		"Документ от 31.02.2022.": "",
	}
	for in, want := range cases {
		if got := DateFromSummary(in, now); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestMatch(t *testing.T) {
	p := catalog.Person{Code: "ИПС", FullName: "Иванов Петр Сергеевич", Aliases: []string{"Иванов П.С.", "Иванов П. С."}, BirthDate: "1950-02-01"}
	cases := map[string]string{
		"Пациент: Иванов Петр Сергеевич":      "полное имя",
		"Пациент: ИВАНОВ   Пётр Сергеевич":    "полное имя", // case, spaces and ё do not matter
		"ФИО = Иванов П.С., 70 лет":           "вариант имени «Иванов П.С.»",
		"Дата рождения: 01.02.1950":           "дата рождения",
		"Иванова Мария, род. 02.02.1950":      "",
		"Ивановский переулок, 1 февраля 1951": "",
	}
	for in, want := range cases {
		if got := Match(p, in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
