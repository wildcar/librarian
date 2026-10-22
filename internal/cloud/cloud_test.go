package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for link, want := range map[string]string{
		"https://disk.yandex.ru/d/AbCdEf123":                            "Яндекс Диск",
		"https://yadi.sk/i/xyz":                                         "Яндекс Диск",
		"https://cloud.example.org/s/aBcD1234eFgH":                      "Nextcloud",
		"https://example.org/nextcloud/index.php/s/aBcD1234eFgH":        "Nextcloud",
		"https://drive.google.com/file/d/1AbCdEfGhIjK/view?usp=sharing": "Google Диск",
		"https://drive.google.com/drive/folders/1AbCdEfGhIjK":           "Google Диск",
		"https://docs.google.com/document/d/1AbCdEfGhIjK/edit":          "Google Диск",
		"https://drive.google.com/open?id=1AbCdEfGhIjK":                 "Google Диск",
	} {
		s, err := Parse(link)
		if err != nil || s.Service() != want {
			t.Errorf("%s: %v %v", link, s, err)
		}
	}
	for _, link := range []string{"", "ftp://disk.yandex.ru/d/x", "https://example.org/file.pdf", "file:///etc/passwd", "https://drive.google.com/"} {
		if _, err := Parse(link); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%q: %v", link, err)
		}
	}
	n, _ := Parse("https://example.org/nextcloud/index.php/s/aBcD1234eFgH")
	if nc := n.(*nextcloud); nc.base != "https://example.org/nextcloud" || nc.token != "aBcD1234eFgH" {
		t.Errorf("nextcloud: %+v", nc)
	}
	d, _ := Parse("https://docs.google.com/spreadsheets/d/1AbCdEfGhIjK/edit#gid=0")
	if g := d.(*google); g.kind != "spreadsheets" || g.id != "1AbCdEfGhIjK" {
		t.Errorf("sheet: %+v", g)
	}
}

func read(t *testing.T, s Share) map[string]string {
	t.Helper()
	files, err := s.Files(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range files {
		r, err := s.Open(context.Background(), f)
		if err != nil {
			t.Fatalf("%s: %v", f.Path, err)
		}
		b, _ := io.ReadAll(r)
		r.Close()
		out[f.Path] = string(b)
	}
	return out
}

func keys(m map[string]string) string {
	var ks []string
	for k, v := range m {
		ks = append(ks, k+"="+v)
	}
	sort.Strings(ks)
	return strings.Join(ks, " ")
}

func TestYandex(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/dl/") {
			fmt.Fprint(w, "body of "+r.URL.Path[4:])
			return
		}
		q := r.URL.Query()
		if q.Get("public_key") != "https://disk.yandex.ru/d/KEY" {
			w.WriteHeader(404)
			return
		}
		file := func(p string) map[string]any {
			return map[string]any{"type": "file", "name": p[strings.LastIndex(p, "/")+1:], "path": p, "size": 3, "file": srv.URL + "/dl" + p}
		}
		var items []map[string]any
		total := 0
		switch q.Get("path") {
		case "", "/": // the root, paged by two
			all := []map[string]any{file("/a.pdf"), {"type": "dir", "name": "Анализы", "path": "/Анализы"}, file("/b.jpg")}
			total = len(all)
			off := 0
			fmt.Sscan(q.Get("offset"), &off)
			items = all[off:min(off+2, len(all))]
		case "/Анализы":
			items, total = []map[string]any{file("/Анализы/c.pdf")}, 1
		}
		json.NewEncoder(w).Encode(map[string]any{"type": "dir", "_embedded": map[string]any{"items": items, "total": total}})
	}))
	defer srv.Close()
	old := yandexAPI
	yandexAPI = srv.URL
	defer func() { yandexAPI = old }()
	s, _ := Parse("https://disk.yandex.ru/d/KEY")
	got := keys(read(t, s))
	if want := "a.pdf=body of a.pdf b.jpg=body of b.jpg Анализы/c.pdf=body of Анализы/c.pdf"; got != want {
		t.Errorf("got %s", got)
	}
	s, _ = Parse("https://disk.yandex.ru/d/GONE")
	if _, err := s.Files(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("closed link: %v", err)
	}
}

func TestNextcloud(t *testing.T) {
	const token = "aBcD1234eFgH"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, _, _ := r.BasicAuth(); u != token && strings.HasPrefix(r.URL.Path, "/nc/public.php") {
			w.WriteHeader(401)
			return
		}
		resp := func(href string, dir bool) string {
			rt := ""
			if dir {
				rt = "<d:collection/>"
			}
			return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop><d:resourcetype>` + rt +
				`</d:resourcetype><d:getcontentlength>3</d:getcontentlength></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
		}
		switch {
		case r.Method == "PROPFIND" && r.URL.Path == "/nc/public.php/webdav/":
			w.WriteHeader(207)
			fmt.Fprint(w, `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">`+resp("/nc/public.php/webdav/", true)+
				resp("/nc/public.php/webdav/%D0%A1%D0%BA%D0%B0%D0%BD%20%201.pdf", false)+resp("/nc/public.php/webdav/Sub/", true)+`</d:multistatus>`)
		case r.Method == "PROPFIND" && r.URL.Path == "/nc/public.php/webdav/Sub":
			w.WriteHeader(207)
			fmt.Fprint(w, `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">`+resp("/nc/public.php/webdav/Sub/", true)+
				resp("/nc/public.php/webdav/Sub/x.jpg", false)+`</d:multistatus>`)
		case r.Method == "GET":
			fmt.Fprint(w, "got "+r.URL.Path)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	old := Client
	Client = srv.Client()
	defer func() { Client = old }()
	s, err := Parse(srv.URL + "/nc/s/" + token)
	if err != nil {
		t.Fatal(err)
	}
	got := keys(read(t, s))
	if want := "Sub/x.jpg=got /nc/public.php/webdav/Sub/x.jpg Скан  1.pdf=got /nc/public.php/webdav/Скан  1.pdf"; got != want {
		t.Errorf("got %s", got)
	}
	s, _ = Parse(srv.URL + "/nc/s/WRONGtoken1")
	if _, err := s.Files(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("closed share: %v", err)
	}
}

func TestGoogle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entry := func(href, name string) string {
			return `<div class="flip-entry"><a href="` + href + `" target="_blank"><div class="flip-entry-info"><div class="flip-entry-title">` + name + `</div></div></a></div>`
		}
		switch {
		case r.URL.Path == "/embeddedfolderview" && r.URL.Query().Get("id") == "FOLDER00001":
			fmt.Fprint(w, entry("https://drive.google.com/file/d/FILE0000001/view?usp=drive_web", "Скан &amp; копия.pdf")+
				entry("https://drive.google.com/drive/folders/FOLDER00002", "Вложенная")+
				entry("https://docs.google.com/document/d/DOC00000001/edit?usp=drive_web", "Заметка"))
		case r.URL.Path == "/embeddedfolderview" && r.URL.Query().Get("id") == "FOLDER00002":
			fmt.Fprint(w, entry("https://drive.google.com/file/d/FILE0000002/view", "фото.jpg")+
				entry("https://drive.google.com/drive/folders/FOLDER00001", "петля"))
		case r.URL.Path == "/download" && r.URL.Query().Get("id") == "CLOSED00001":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html>sign in</html>")
		case r.URL.Path == "/download":
			w.Header().Set("Content-Disposition", `attachment; filename="x"; filename*=UTF-8''%D1%81%D0%BA%D0%B0%D0%BD.pdf`)
			fmt.Fprint(w, "file "+r.URL.Query().Get("id"))
		case strings.HasPrefix(r.URL.Path, "/document/d/"):
			fmt.Fprint(w, "pdf of "+r.URL.Path)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	oldD, oldDocs, oldF := googleDownload, googleDocs, googleFolders
	googleDownload, googleDocs, googleFolders = srv.URL+"/download", srv.URL, srv.URL+"/embeddedfolderview"
	defer func() { googleDownload, googleDocs, googleFolders = oldD, oldDocs, oldF }()

	s, _ := Parse("https://drive.google.com/drive/folders/FOLDER00001?usp=sharing")
	got := keys(read(t, s))
	if want := "Вложенная/фото.jpg=file FILE0000002 Заметка.pdf=pdf of /document/d/DOC00000001/export Скан & копия.pdf=file FILE0000001"; got != want {
		t.Errorf("folder: %s", got)
	}
	s, _ = Parse("https://drive.google.com/file/d/FILE0000003/view")
	if got := keys(read(t, s)); got != "скан.pdf=file FILE0000003" {
		t.Errorf("file: %s", got)
	}
	s, _ = Parse("https://drive.google.com/file/d/CLOSED00001/view")
	if _, err := s.Files(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("closed file: %v", err)
	}
}
