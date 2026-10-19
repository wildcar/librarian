package report

import "testing"

func TestPath(t *testing.T) {
	cases := map[string]string{
		"file:///C:/Users/x/Сканы/a.pdf": "C:/Users/x/Сканы/a.pdf",
		"file:///home/x/a b.pdf":         "/home/x/a b.pdf",
	}
	for in, want := range cases {
		if got := Path(in); got != want {
			t.Errorf("Path(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBytes(t *testing.T) {
	cases := map[int64]string{0: "0 Б", 1023: "1023 Б", 1536: "1.5 КБ", 51 << 20: "51.0 МБ"}
	for in, want := range cases {
		if got := Bytes(in); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", in, got, want)
		}
	}
}
