package cloud

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// A Nextcloud (or ownCloud) public share: https://host[/prefix][/index.php]/s/<token>.
var nextcloudShare = regexp.MustCompile(`^(.*?)(?:/index\.php)?/s/([A-Za-z0-9]{8,64})/?$`)

type nextcloud struct {
	base  string // scheme://host[/prefix]
	token string
}

func parseNextcloud(u *url.URL) Share {
	m := nextcloudShare.FindStringSubmatch(u.EscapedPath())
	if m == nil {
		return nil
	}
	return &nextcloud{base: u.Scheme + "://" + u.Host + m[1], token: m[2]}
}

func (n *nextcloud) Service() string { return "Nextcloud" }

// dav is the share's public WebDAV root: the token is the user name.
func (n *nextcloud) dav() string     { return n.base + "/public.php/webdav" }
func (n *nextcloud) auth() [2]string { return [2]string{n.token, ""} }

type davResponse struct {
	Href  string `xml:"href"`
	Props []struct {
		Prop struct {
			Collection *struct{} `xml:"resourcetype>collection"`
			Length     string    `xml:"getcontentlength"`
		} `xml:"prop"`
		Status string `xml:"status"`
	} `xml:"propstat"`
}

func (n *nextcloud) list(ctx context.Context, dir string) ([]davResponse, error) {
	body := `<?xml version="1.0"?><d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><d:getcontentlength/></d:prop></d:propfind>`
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", n.dav()+escapePath(dir), strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(n.token, "")
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml")
	resp, err := Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, ErrClosed
	case resp.StatusCode != http.StatusMultiStatus:
		return nil, fmt.Errorf("Nextcloud: %s", resp.Status)
	}
	var ms struct {
		Responses []davResponse `xml:"response"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&ms); err != nil {
		return nil, fmt.Errorf("Nextcloud: %w", err)
	}
	return ms.Responses, nil
}

func (n *nextcloud) Files(ctx context.Context) ([]File, error) {
	davPath := mustPath(n.dav())
	var out []File
	dirs := []string{"/"}
	for len(dirs) > 0 {
		dir := dirs[0]
		dirs = dirs[1:]
		rs, err := n.list(ctx, dir)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			href, err := url.PathUnescape(r.Href)
			if err != nil {
				continue
			}
			rel := strings.TrimPrefix(strings.TrimPrefix(href, davPath), "/")
			collection, size := false, int64(-1)
			for _, p := range r.Props {
				if strings.Contains(p.Status, " 200 ") {
					collection = p.Prop.Collection != nil
					if v, err := strconv.ParseInt(p.Prop.Length, 10, 64); err == nil {
						size = v
					}
				}
			}
			switch {
			case strings.TrimSuffix(rel, "/") == strings.Trim(dir, "/"):
				// The folder itself, or a single-file share's only file.
				if !collection && dir == "/" {
					name, err := n.sharedName(ctx)
					if err != nil {
						return nil, err
					}
					return []File{{Path: name, Size: size, url: n.dav() + "/", auth: n.auth()}}, nil
				}
			case collection:
				dirs = append(dirs, "/"+strings.TrimSuffix(rel, "/"))
			default:
				out = append(out, File{Path: rel, Size: size, url: n.dav() + escapePath("/"+rel), auth: n.auth()})
			}
		}
		if len(out) > MaxFiles {
			return nil, tooMany()
		}
	}
	return out, nil
}

// sharedName learns a single shared file's name, which its WebDAV root does
// not carry, from the share's download answer.
func (n *nextcloud) sharedName(ctx context.Context) (string, error) {
	resp, err := get(ctx, n.base+"/s/"+n.token+"/download", [2]string{}, "Range", "bytes=0-0")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if name := attachmentName(resp.Header); name != "" {
		return name, nil
	}
	return n.token, nil
}

func (n *nextcloud) Open(ctx context.Context, f File) (io.ReadCloser, error) { return download(ctx, f) }

func escapePath(p string) string {
	return (&url.URL{Path: p}).EscapedPath()
}

func mustPath(u string) string {
	p, _ := url.Parse(u)
	return path.Clean(p.Path)
}
