// Package mail sends the service's letters (ADR-0007): plain UTF-8 text over
// SMTP, by default to the local Postfix.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Sender sends one letter.
type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// SMTP sends through a server that takes the service's mail: Addr host:port,
// with a login only when User is set (the server must then offer TLS).
type SMTP struct {
	Addr, User, Password, From string
}

// Send delivers one letter.
func (s SMTP) Send(ctx context.Context, to, subject, body string) error {
	msg, err := Compose(s.From, to, subject, body, time.Now())
	if err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mail: %w", err)
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok && s.User != "" {
		if err := c.StartTLS(nil); err != nil {
			return fmt.Errorf("mail: %w", err)
		}
	}
	if s.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.User, s.Password, host)); err != nil {
			return fmt.Errorf("mail: %w", err)
		}
	}
	if err := c.Mail(s.From); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("mail to: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	return c.Quit()
}

// Compose builds the letter: headers and a quoted-printable UTF-8 body.
func Compose(from, to, subject, body string, at time.Time) ([]byte, error) {
	for _, h := range []string{from, to, subject} {
		if strings.ContainsAny(h, "\r\n") {
			return nil, fmt.Errorf("mail: line break in a header")
		}
	}
	id := make([]byte, 12)
	rand.Read(id)
	domain := from[strings.LastIndexByte(from, '@')+1:]
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", mime.QEncoding.Encode("utf-8", "Библиотека")+" <"+from+">")
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", at.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), domain)
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n\r\n")
	q := quotedprintable.NewWriter(&b)
	io.WriteString(q, strings.ReplaceAll(body, "\n", "\r\n"))
	q.Close()
	return b.Bytes(), nil
}

// Log "sends" by writing the letter to w: for development without a mail
// server. Never use it in production, the letter holds a login link.
type Log struct{ W io.Writer }

// Send writes the letter.
func (l Log) Send(_ context.Context, to, subject, body string) error {
	_, err := fmt.Fprintf(l.W, "--- письмо для %s: %s\n%s\n---\n", to, subject, body)
	return err
}
