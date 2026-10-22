// Package cloud reads public share links (ADR-0007): Yandex Disk and
// Nextcloud shares, files and folders, and Google Drive files and folders.
// It only lists and downloads; nothing is ever written to the share.
package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// MaxFiles bounds one share, so a link to a huge disk cannot run forever.
const MaxFiles = 5000

// File is one file of a share.
type File struct {
	Path string // inside the share, "/"-separated; the file's name for a single-file share
	Size int64  // -1 when the service does not say
	url  string // where it downloads from
	auth [2]string
}

// Share is a public link opened for reading.
type Share interface {
	// Service names it for people: «Яндекс Диск», …
	Service() string
	// Files lists every file, folders walked down.
	Files(ctx context.Context) ([]File, error)
	// Open downloads one file.
	Open(ctx context.Context, f File) (io.ReadCloser, error)
}

// ErrUnsupported is a link of no service this package reads.
var ErrUnsupported = errors.New("такие ссылки библиотека не читает: нужна публичная ссылка Яндекс Диска, Nextcloud или Google Диска")

// ErrClosed is a link that does not open: removed, private or protected by a password.
var ErrClosed = errors.New("ссылка не открывается: доступ закрыт, она удалена или защищена паролем")

// Client is the HTTP client shares use; tests replace it.
var Client = &http.Client{Timeout: 30 * time.Minute}

// Parse recognises a link. It does not touch the network.
func Parse(link string) (Share, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, ErrUnsupported
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "yadi.sk" || strings.HasPrefix(host, "disk.yandex.") || strings.HasPrefix(host, "disk.360.yandex."):
		return &yandex{key: u.String(), api: yandexAPI}, nil
	case host == "drive.google.com" || host == "docs.google.com":
		return parseGoogle(u)
	}
	if s := parseNextcloud(u); s != nil {
		return s, nil
	}
	return nil, ErrUnsupported
}

func get(ctx context.Context, u string, auth [2]string, header ...string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if auth[0] != "" {
		req.SetBasicAuth(auth[0], auth[1])
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		return nil, ErrClosed
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", redact(u), resp.Status)
	}
	return resp, nil
}

// redact keeps a share's address out of errors only down to its host: the
// link itself is the key to the share.
func redact(u string) string {
	if p, err := url.Parse(u); err == nil {
		return p.Scheme + "://" + p.Host
	}
	return "ссылка"
}

// download opens a file's body.
func download(ctx context.Context, f File) (io.ReadCloser, error) {
	resp, err := get(ctx, f.url, f.auth)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// attachmentName is the file name a download response gives.
func attachmentName(h http.Header) string {
	_, ps, err := mime.ParseMediaType(h.Get("Content-Disposition"))
	if err != nil {
		return ""
	}
	return path.Base(strings.ReplaceAll(ps["filename"], "\\", "/"))
}

func tooMany() error {
	return fmt.Errorf("в ссылке больше %d файлов: дайте ссылку на папку поменьше", MaxFiles)
}
