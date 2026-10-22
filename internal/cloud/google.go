package cloud

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/url"
	"regexp"
	"strings"
)

// Google Drive and Docs links. Files download without a key; a folder is
// read from its public embedded view, the only listing Google gives without
// an API key.
var (
	googleFolder = regexp.MustCompile(`/folders/([A-Za-z0-9_-]{10,})`)
	googleDoc    = regexp.MustCompile(`^/(document|spreadsheets|presentation)/d/([A-Za-z0-9_-]{10,})`)
	googleFileID = regexp.MustCompile(`/file/d/([A-Za-z0-9_-]{10,})`)
	googleEntry  = regexp.MustCompile(`(?s)<a href="([^"]+)"[^>]*>.*?<div class="flip-entry-title">([^<]*)</div>`)
)

// Endpoints, replaced in tests.
var (
	googleDownload = "https://drive.usercontent.google.com/download"
	googleDocs     = "https://docs.google.com"
	googleFolders  = "https://drive.google.com/embeddedfolderview"
)

type google struct {
	kind string // file | folder | document | spreadsheets | presentation
	id   string
}

func parseGoogle(u *url.URL) (Share, error) {
	p := u.EscapedPath()
	if m := googleDoc.FindStringSubmatch(p); m != nil && u.Hostname() == "docs.google.com" {
		return &google{kind: m[1], id: m[2]}, nil
	}
	if m := googleFolder.FindStringSubmatch(p); m != nil {
		return &google{kind: "folder", id: m[1]}, nil
	}
	if m := googleFileID.FindStringSubmatch(p); m != nil {
		return &google{kind: "file", id: m[1]}, nil
	}
	if id := u.Query().Get("id"); id != "" && (p == "/open" || p == "/uc") {
		return &google{kind: "file", id: id}, nil
	}
	return nil, ErrUnsupported
}

func (g *google) Service() string { return "Google Диск" }

// fileURL is where a listed item downloads from: a file as it is, a Google
// document exported as PDF.
func fileURL(kind, id string) string {
	switch kind {
	case "document", "spreadsheets":
		return googleDocs + "/" + kind + "/d/" + id + "/export?format=pdf"
	case "presentation":
		return googleDocs + "/presentation/d/" + id + "/export/pdf"
	}
	return googleDownload + "?" + url.Values{"id": {id}, "export": {"download"}, "confirm": {"t"}}.Encode()
}

func (g *google) Files(ctx context.Context) ([]File, error) {
	if g.kind != "folder" {
		f := File{Size: -1, url: fileURL(g.kind, g.id)}
		name, err := googleName(ctx, f.url)
		if err != nil {
			return nil, err
		}
		f.Path = name
		return []File{f}, nil
	}
	var out []File
	type dir struct{ id, path string }
	dirs := []dir{{g.id, ""}}
	seen := map[string]bool{g.id: true}
	for len(dirs) > 0 {
		d := dirs[0]
		dirs = dirs[1:]
		resp, err := get(ctx, googleFolders+"?id="+url.QueryEscape(d.id), [2]string{})
		if err != nil {
			return nil, err
		}
		page, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, m := range googleEntry.FindAllStringSubmatch(string(page), -1) {
			href, name := html.UnescapeString(m[1]), strings.TrimSpace(html.UnescapeString(m[2]))
			hu, err := url.Parse(href)
			if err != nil || name == "" {
				continue
			}
			p := strings.TrimPrefix(d.path+"/"+name, "/")
			switch {
			case googleFolder.MatchString(hu.Path):
				id := googleFolder.FindStringSubmatch(hu.Path)[1]
				if !seen[id] {
					seen[id] = true
					dirs = append(dirs, dir{id, p})
				}
			case googleDoc.MatchString(hu.Path):
				m := googleDoc.FindStringSubmatch(hu.Path)
				out = append(out, File{Path: p + ".pdf", Size: -1, url: fileURL(m[1], m[2])})
			case googleFileID.MatchString(hu.Path):
				out = append(out, File{Path: p, Size: -1, url: fileURL("file", googleFileID.FindStringSubmatch(hu.Path)[1])})
			}
		}
		if len(out) > MaxFiles {
			return nil, tooMany()
		}
	}
	return out, nil
}

// googleName asks for a file's first byte to learn its name. Google answers
// a closed file with a sign-in or warning page instead of the file.
func googleName(ctx context.Context, u string) (string, error) {
	resp, err := get(ctx, u, [2]string{}, "Range", "bytes=0-0")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	name := attachmentName(resp.Header)
	if name == "" {
		return "", ErrClosed
	}
	return name, nil
}

func (g *google) Open(ctx context.Context, f File) (io.ReadCloser, error) {
	resp, err := get(ctx, f.url, [2]string{})
	if err != nil {
		return nil, err
	}
	if attachmentName(resp.Header) == "" && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %w", f.Path, ErrClosed)
	}
	return resp.Body, nil
}
