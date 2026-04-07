package smtp

import (
	"io"
	"log"
	"mime"
	"strings"
	"time"

	gsmtp "github.com/emersion/go-smtp"
	gomail "github.com/emersion/go-message/mail"

	"tempmail/internal/mailbox"
	"tempmail/internal/mailstore"
)

type backend struct{}
type session struct {
	to string
}

func (b *backend) NewSession(_ *gsmtp.Conn) (gsmtp.Session, error) {
	return &session{}, nil
}

func (s *session) AuthPlain(_, _ string) error { 
	// Reject all authentication attempts; this is a public relay for local testing only
	return gsmtp.ErrAuthUnsupported 
}
func (s *session) Mail(_ string, _ *gsmtp.MailOptions) error { return nil }

func (s *session) Rcpt(to string, _ *gsmtp.RcptOptions) error {
	s.to = strings.ToLower(to)
	return nil
}

func (s *session) Data(r io.Reader) error {
	if s.to == "" {
		return nil
	}

	const maxSize = 10 << 20
	limited := io.LimitReader(r, maxSize)

	mr, err := gomail.CreateReader(limited)
	if err != nil {
		log.Printf("smtp: parse error: %v", err)
		return nil
	}

	m := &mailstore.Mail{
		To:   s.to,
		Date: time.Now(),
	}

	h := mr.Header
	if from, err := h.AddressList("From"); err == nil && len(from) > 0 {
		m.From = from[0].String()
	}
	if subject, err := h.Subject(); err == nil {
		m.Subject = subject
	}
	if date, err := h.Date(); err == nil {
		m.Date = date
	}

	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		// Determine content-type from the raw header
		ct := p.Header.Get("Content-Type")
		mediaType, _, _ := mime.ParseMediaType(ct)

		switch p.Header.(type) {
		case *gomail.InlineHeader:
			body, _ := io.ReadAll(p.Body)
			switch mediaType {
			case "text/plain":
				m.Text = string(body)
			case "text/html":
				m.HTML = string(body)
			}
		case *gomail.AttachmentHeader:
			ah := p.Header.(*gomail.AttachmentHeader)
			filename, _ := ah.Filename()
			body, _ := io.ReadAll(p.Body)
			m.Attachments = append(m.Attachments, mailstore.Attachment{
				Filename:    filename,
				Size:        len(body),
				ContentType: mediaType,
			})
		}
	}

	id := strings.Split(s.to, "@")[0]
	mailstore.Save(s.to, m)
	mailbox.GetHub().Push(id, map[string]any{
		"type": "mail",
		"mail": m,
	})
	return nil
}

func (s *session) Reset()        { s.to = "" }
func (s *session) Logout() error { return nil }

// NewServer creates a configured SMTP server.
func NewServer() *gsmtp.Server {
	srv := gsmtp.NewServer(&backend{})
	srv.Domain = "localhost"
	srv.AllowInsecureAuth = true
	srv.MaxMessageBytes = 10 << 20
	return srv
}
