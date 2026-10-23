package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wildcar/librarian/internal/catalog"
	"github.com/wildcar/librarian/internal/cloud"
	"github.com/wildcar/librarian/internal/index"
	"github.com/wildcar/librarian/internal/intake"
	"github.com/wildcar/librarian/internal/pipeline"
)

// MaxAddBytes caps a file sent through add_document: it travels inside one
// JSON request as base64.
const MaxAddBytes = 20 << 20

// Intake is the background worker that takes new files to search and
// downloads cloud links (pipeline.Worker).
type Intake interface {
	Kick()
	IngestLink(accountID, link string, l pipeline.Labels)
}

// SetIntake lets add_document hand files and links to the worker; without it
// files are kept and wait for the next `librarian run`, links are refused.
func (s *Server) SetIntake(w Intake) { s.intake = w }

type addIn struct {
	Reason  string   `json:"reason" jsonschema:"зачем: что сказал пользователь, его словами"`
	Name    string   `json:"name,omitempty" jsonschema:"имя файла с расширением, например «анализ крови.pdf»; для content и text"`
	Content string   `json:"content,omitempty" jsonschema:"содержимое файла в base64, до 20 МБ"`
	Text    string   `json:"text,omitempty" jsonschema:"текст документа, если это текст, а не файл"`
	Link    string   `json:"link,omitempty" jsonschema:"публичная ссылка на файл или папку: Яндекс Диск, Nextcloud, Google Drive"`
	Persons []string `json:"persons,omitempty" jsonschema:"коды людей из list_persons, чьи это документы"`
	Attrs   []string `json:"attrs,omitempty" jsonschema:"характеристики «ключ=значение» для всех документов из файла или ссылки"`
	Label   string   `json:"label,omitempty" jsonschema:"короткая подпись пачки, например «анализы октябрь»"`
}

type addOut struct {
	Batch   int64  `json:"batch,omitempty"`
	Known   bool   `json:"known,omitempty"`
	Queued  bool   `json:"queued,omitempty"`
	Message string `json:"message"`
}

func (t *tools) add(ctx context.Context, req *mcp.CallToolRequest, in addIn) (*mcp.CallToolResult, addOut, error) {
	if strings.TrimSpace(in.Reason) == "" {
		return nil, addOut{}, errors.New("reason обязателен: что сказал пользователь")
	}
	given := 0
	for _, s := range []string{in.Content, in.Text, in.Link} {
		if s != "" {
			given++
		}
	}
	if given != 1 {
		return nil, addOut{}, errors.New("нужно ровно одно из content, text, link")
	}
	l := pipeline.Labels{Persons: in.Persons, Label: strings.TrimSpace(in.Label),
		Actor: assistantOf(req), Reason: strings.TrimSpace(in.Reason)}
	for _, s := range in.Attrs {
		a, err := catalog.ParseAttr(s)
		if err != nil {
			return nil, addOut{}, err
		}
		l.Attrs = append(l.Attrs, a)
	}
	if err := t.knownPersons(ctx, in.Persons); err != nil {
		return nil, addOut{}, err
	}

	if in.Link != "" {
		if _, err := cloud.Parse(in.Link); err != nil {
			return nil, addOut{}, fmt.Errorf("link: %w", err)
		}
		if t.intake == nil {
			return nil, addOut{}, errors.New("фоновая обработка на сервере выключена: ссылку сейчас не принять")
		}
		t.intake.IngestLink(t.acc.ID, in.Link, l)
		return nil, addOut{Queued: true, Message: "Ссылка в очереди: библиотека скачает файлы, прочитает их, " +
			"и через несколько минут документы появятся в search_documents."}, nil
	}

	name := filepath.Base(strings.ReplaceAll(strings.TrimSpace(in.Name), "\\", "/"))
	var body []byte
	if in.Text != "" {
		body = []byte(in.Text)
		if name == "." || name == "" {
			name = "текст.txt"
		} else if filepath.Ext(name) == "" {
			name += ".txt"
		}
	} else {
		if name == "." || name == "" {
			return nil, addOut{}, errors.New("name обязателен для content: по расширению видно, что это за файл")
		}
		if base64.StdEncoding.DecodedLen(len(in.Content)) > MaxAddBytes+3 {
			return nil, addOut{}, fmt.Errorf("файл больше %d МБ: пришлите его ссылкой (link)", MaxAddBytes>>20)
		}
		var err error
		if body, err = decode64(in.Content); err != nil {
			return nil, addOut{}, errors.New("content: не base64")
		}
	}
	st, err := intake.Store(ctx, t.cat, t.acc.Root(), name, bytes.NewReader(body), MaxAddBytes)
	if err != nil {
		return nil, addOut{}, err
	}
	now := time.Now()
	b := catalog.Batch{Kind: "upload", Label: l.Label, Persons: l.Persons, Attrs: l.Attrs, Actor: l.Actor, Reason: l.Reason}
	if b.ID, err = t.cat.CreateBatch(ctx, b, []string{st.SHA256}, now); err != nil {
		return nil, addOut{}, err
	}
	// A file already in the library gets the labels at once.
	if len(l.Persons) > 0 || len(l.Attrs) > 0 {
		if _, err := index.Run(ctx, t.cat, now); err != nil {
			return nil, addOut{}, err
		}
	}
	out := addOut{Batch: b.ID, Known: !st.New}
	switch {
	case !st.New:
		out.Message = "Этот файл уже есть в библиотеке; подписи добавлены к его документам (пачка " + fmt.Sprint(b.ID) + ")."
	case t.intake != nil:
		t.intake.Kick()
		out.Message = "Файл принят (пачка " + fmt.Sprint(b.ID) + "): библиотека прочитает его, и через несколько минут " +
			"документы появятся в search_documents с batch=" + fmt.Sprint(b.ID) + "."
	default:
		out.Message = "Файл сохранён (пачка " + fmt.Sprint(b.ID) + "), но фоновая обработка выключена: в поиск он попадёт после обработки владельцем."
	}
	return nil, out, nil
}

// decode64 takes standard or URL-safe base64, padded or not, with line breaks.
func decode64(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
	if i := strings.Index(s, ";base64,"); i >= 0 && strings.HasPrefix(s, "data:") {
		s = s[i+len(";base64,"):]
	}
	s = strings.TrimRight(s, "=")
	if strings.ContainsAny(s, "-_") {
		return base64.RawURLEncoding.DecodeString(s)
	}
	return base64.RawStdEncoding.DecodeString(s)
}

func (t *tools) knownPersons(ctx context.Context, codes []string) error {
	if len(codes) == 0 {
		return nil
	}
	ps, err := t.cat.Persons(ctx)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, p := range ps {
		known[p.Code] = true
	}
	for _, c := range codes {
		if !known[c] {
			return fmt.Errorf("человека с кодом %s нет: коды — в list_persons", c)
		}
	}
	return nil
}
