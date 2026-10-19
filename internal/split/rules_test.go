package split

import (
	"strings"
	"testing"
)

func TestBoundary(t *testing.T) {
	protocolEnd := "Заключение: без патологии.\nПодпись врача: Иванов И.И.\nДата: 02.04.2022"
	cases := []struct {
		name     string
		a, b     string
		new      bool
		strong   bool
		contains string
	}{
		{"running page number",
			"Возраст: 76 Пациент Иванов 2\nГрафик ЧСС", "Возраст: 76 Пациент Иванов 3\nСтатистика", false, true, "running"},
		{"printout after printout",
			"SIEMENS\n11:25:42 02.04.2022\nD=8.6 mm\n5cm", "SIEMENS\n11:28:16 02.04.2022\nD=3.9 mm\n5cm", false, true, "printout after printout"},
		{"protocol after its printouts",
			"SIEMENS\nD=8.6 mm\n5cm", "СМ-Клиника\nПротокол обследования\nПациент", false, true, "protocol after"},
		{"printouts from another device",
			"Kenz C1211\nD=1 mm\nMI 1", "GEMS IT MAC1200\nD=2 mm\nMI 2", true, false, "another device"},
		{"same letterhead, OCR variant (weak)",
			"ДМС\nПЕРЕДОВЫЕ ТЕХНОЛОГИИ\nРезультаты холтеровского мониторирования ЭКГ\nтекст",
			"АМС\nПЕРЕДОВЫЕ ТЕХНОЛОГИИ\nРезультаты холтеровского мониторирования ЭКГ\nтекст", false, false, "letterhead"},
		{"table continues", "| 1 | 2 |", "| 02.06.2026 | Анализ | 320,00 |", false, true, "mid-document"},
		{"lowercase start after a date line", "Пищевод: вход свободный", "02.04.22\nрт.ст. давление в норме", false, true, "mid-document"},
		{"title after an ending", protocolEnd, "СМ-Клиника\nРЕКОМЕНДАЦИИ ПО ЛЕЧЕНИЮ\nПациент", true, true, "title"},
		{"title without an ending", "Пищевод: вход свободный", "Выписной эпикриз\nПациент", true, false, "title"},
		{"nothing to go by", "Текст первой страницы", "Текст второй страницы", false, false, "no signal"},
	}
	for _, c := range cases {
		v := Boundary(c.a, c.b)
		if v.New != c.new || v.Strong != c.strong || !strings.Contains(v.Reason, c.contains) {
			t.Errorf("%s: got %+v, want new=%v strong=%v reason~%q", c.name, v, c.new, c.strong, c.contains)
		}
	}
}

func TestRatio(t *testing.T) {
	if r := ratio("ДМС ПЕРЕДОВЫЕ", "АМС ПЕРЕДОВЫЕ"); r < 0.9 || r >= 1 {
		t.Errorf("one-letter OCR variant: %v", r)
	}
	if ratio("", "") != 1 || ratio("abc", "abc") != 1 || ratio("abc", "xyz") != 0 {
		t.Error("ratio edge cases")
	}
}

func TestParseStarts(t *testing.T) {
	got, err := parseStarts("Вот ответ:\n```json\n{\"starts\": [3, 1, 3]}\n```", 4)
	if err != nil || len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Errorf("parseStarts = %v, %v", got, err)
	}
	if _, err := parseStarts(`{"starts": [5]}`, 4); err == nil {
		t.Error("page outside the window accepted")
	}
	if _, err := parseStarts("не знаю", 4); err == nil {
		t.Error("reply without JSON accepted")
	}
	if got, _ := parseStarts(`{"starts": []}`, 4); len(got) != 1 || got[0] != 1 {
		t.Errorf("empty starts should mean one document: %v", got)
	}
}
