package layers

import (
	"strings"
	"testing"
)

func TestEssentialSafeguards(t *testing.T) {
	lines := []string{
		"Возраст: 76 Иванов Иван Иванович 2", // running header: the only place the name is
		"Статистика нарушений ритма",
		"Возраст: 76 Иванов Иван Иванович 3",
		"Почасовая таблица",
		"Звоните +7 (4912) 77-67-51, запись онлайн",       // contact: may go
		"Дата посещения: 02.06.2026 Следующее посещение:", // a date: must stay
		"Нужна операция? Консультация московского врача",  // advert with a medical word: comes back, harmless
		"ДЕК 4, 2019 20:26 HF, DFw",                       // a device line, but the only date of the ECG
	}
	drop := map[int]string{1: "колонтитул", 3: "колонтитул", 5: "контакты", 6: "пустые поля", 7: "реклама", 8: "служебная строка"}
	kept, rr, rs := Essential(lines, drop)
	got := strings.Join(kept, "\n")
	if rr != 1 || !strings.Contains(got, "Иванов Иван Иванович 2") || strings.Contains(got, "Иванович 3") {
		t.Errorf("repeat safeguard: restored %d\n%s", rr, got)
	}
	if !strings.Contains(got, "Дата посещения") {
		t.Errorf("a dated line was lost:\n%s", got)
	}
	if strings.Contains(got, "77-67-51") {
		t.Errorf("a contact line came back:\n%s", got)
	}
	if rs != 3 || !strings.Contains(got, "ДЕК 4, 2019") {
		t.Errorf("suspicious restored %d, want 3 (two dates, the advert)", rs)
	}
	if len(drop) != 6 {
		t.Error("Essential modified the caller's map")
	}
}

func TestEssentialKeepsEveryDate(t *testing.T) {
	// Same shape, different dates: the second must not hide behind the first.
	lines := []string{"Дата: 02.04.2022", "Анализ крови", "Дата: 03.04.2022", "12:50:13 05.12.2019", "12:49:06 05.12.2019"}
	kept, _, _ := Essential(lines, map[int]string{3: "повтор", 4: "аппарат", 5: "аппарат"})
	got := strings.Join(kept, "\n")
	if !strings.Contains(got, "03.04.2022") || !strings.Contains(got, "05.12.2019") {
		t.Errorf("a date was lost:\n%s", got)
	}
	if strings.Count(got, "05.12.2019") != 1 {
		t.Errorf("repeated timestamps of one date should come back once:\n%s", got)
	}
}

func TestForbiddenAndLeaks(t *testing.T) {
	src := `ГБУ РО "Рязанский областной онкологический диспансер"
Пациент: Иванов Петр Сергеевич, 01.02.1950 г.р.
Врач: Сидорова А.Б.
СМ-Клиника`
	f := Forbidden(src, "Петров Василий")
	for _, want := range []string{"иван", "петр сергеевич", "сидоро", "роод", "см-клиника", "петров василий"} {
		found := false
		for _, s := range f {
			if strings.Contains(s, want) || s == want {
				found = true
			}
		}
		if !found {
			t.Errorf("forbidden misses %q: %v", want, f)
		}
	}
	clean := "Это рекомендации онколога областного диспансера от 12.08.2026. Назначено обследование."
	if l := Leaks(clean, f); len(l) != 0 {
		t.Errorf("clean summary flagged: %v", l)
	}
	dirty := "Пациент Иванов направлен в РООД, звонить +7 (4912) 77-67-51, карта АМ1234567."
	l := strings.Join(Leaks(dirty, f), " | ")
	for _, want := range []string{"иван", "роод", "телефон", "номер"} {
		if !strings.Contains(l, want) {
			t.Errorf("leak %q not found in %q", want, l)
		}
	}
	// "врождённый" is a medical fact, not a birth date.
	if l := Leaks("Врождённый порок сердца.", f); len(l) != 0 {
		t.Errorf("medical word flagged: %v", l)
	}
}

func TestInventedNumbers(t *testing.T) {
	src := "Дата: 6.10.26\nПрием 15⁰⁰ таблетка 150 мг\n2 06.26 визит\nвыписан 31 марта 2022 г."
	ok := "Документ от 06.10.2026: в 15:00 приём 150 мг; визит 02.06.2026; выписка 31.03.2022."
	if inv := InventedNumbers(ok, src); len(inv) != 0 {
		t.Errorf("reformatted values flagged: %v", inv)
	}
	// Shapes seen in real scans: abbreviated and upper-case months, month
	// first, Roman months with Cyrillic Х, quoted days.
	src2 := "21.Янв.1970 00:17\nЭКГ ДЕК 4, 2019 20:26\nДата «29» Х 2019г."
	ok2 := "ЭКГ от 21 января 1970 и от 04.12.2019, дата 29 октября 2019."
	if inv := InventedNumbers(ok2, src2); len(inv) != 0 {
		t.Errorf("real-scan date shapes flagged: %v", inv)
	}
	// A rejected candidate must not swallow the date after it; a size is no date.
	src3 := "Скидка 0% Скидка 09.06.2026 действующий\nобразование 1,5 × 9,12 мм"
	ok3 := "Соглашение от 09.06.2026; образование 1,5 × 9,12 мм."
	if inv := InventedNumbers(ok3, src3); len(inv) != 0 {
		t.Errorf("overlap or size misread: %v", inv)
	}
	bad := "Документ от 18.11.2024, доза 250 мг."
	inv := strings.Join(InventedNumbers(bad, src), " ")
	if !strings.Contains(inv, "18.11.2024") || !strings.Contains(inv, "250") {
		t.Errorf("invented values not found: %q", inv)
	}
}

func TestParseRemove(t *testing.T) {
	got, err := parseRemove("```json\n{\"remove\": [[2, \"реклама\"], [9, \"x\"], 3, [\"bad\"]]}\n```", 5)
	if err != nil || len(got) != 2 || got[2] != "реклама" || got[3] != "" {
		t.Errorf("parseRemove = %v, %v", got, err)
	}
	if _, err := parseRemove("no json", 5); err == nil {
		t.Error("reply without JSON accepted")
	}
}
