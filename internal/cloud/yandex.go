package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// yandexAPI is the public part of the Yandex Disk REST API; no token needed.
var yandexAPI = "https://cloud-api.yandex.net/v1/disk/public/resources"

type yandex struct {
	key string // the public link itself is the key
	api string
}

func (y *yandex) Service() string { return "Яндекс Диск" }

type yandexItem struct {
	Type     string `json:"type"` // file | dir
	Name     string `json:"name"`
	Path     string `json:"path"` // inside the share, from "/"
	Size     int64  `json:"size"`
	File     string `json:"file"` // direct download
	Embedded *struct {
		Items  []yandexItem `json:"items"`
		Total  int          `json:"total"`
		Offset int          `json:"offset"`
		Limit  int          `json:"limit"`
	} `json:"_embedded"`
}

func (y *yandex) page(ctx context.Context, dir string, offset int) (*yandexItem, error) {
	q := url.Values{"public_key": {y.key}, "limit": {"200"}, "offset": {fmt.Sprint(offset)}}
	if dir != "" {
		q.Set("path", dir)
	}
	resp, err := get(ctx, y.api+"?"+q.Encode(), [2]string{})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var it yandexItem
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&it); err != nil {
		return nil, fmt.Errorf("Яндекс Диск: %w", err)
	}
	return &it, nil
}

func (y *yandex) Files(ctx context.Context) ([]File, error) {
	root, err := y.page(ctx, "", 0)
	if err != nil {
		return nil, err
	}
	if root.Type == "file" {
		return []File{{Path: root.Name, Size: root.Size, url: root.File}}, nil
	}
	var out []File
	dirs := []string{"/"}
	for len(dirs) > 0 {
		dir := dirs[0]
		dirs = dirs[1:]
		for offset := 0; ; {
			p := root
			if dir != "/" || offset > 0 {
				if p, err = y.page(ctx, dir, offset); err != nil {
					return nil, err
				}
			}
			if p.Embedded == nil {
				break
			}
			for _, it := range p.Embedded.Items {
				switch it.Type {
				case "dir":
					dirs = append(dirs, it.Path)
				case "file":
					out = append(out, File{Path: strings.TrimPrefix(it.Path, "/"), Size: it.Size, url: it.File})
				}
			}
			if len(out) > MaxFiles {
				return nil, tooMany()
			}
			offset += len(p.Embedded.Items)
			if len(p.Embedded.Items) == 0 || offset >= p.Embedded.Total {
				break
			}
		}
	}
	return out, nil
}

// Open asks for a fresh download link when the listed one is missing; the
// listed ones are signed and live for hours.
func (y *yandex) Open(ctx context.Context, f File) (io.ReadCloser, error) {
	if f.url == "" {
		q := url.Values{"public_key": {y.key}, "path": {"/" + f.Path}}
		resp, err := get(ctx, y.api+"/download?"+q.Encode(), [2]string{})
		if err != nil {
			return nil, err
		}
		var l struct {
			Href string `json:"href"`
		}
		err = json.NewDecoder(resp.Body).Decode(&l)
		resp.Body.Close()
		if err != nil || l.Href == "" {
			return nil, fmt.Errorf("Яндекс Диск не дал скачать %s", f.Path)
		}
		f.url = l.Href
	}
	return download(ctx, f)
}
