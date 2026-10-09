package notify

import (
	"context"
	"fmt"
	"net/smtp"
)

// SMTP is used for local development/e2e with Mailpit; production uses SES.
type SMTP struct{ address, from string }

func NewSMTP(address, from string) *SMTP { return &SMTP{address: address, from: from} }
func (s *SMTP) Send(ctx context.Context, mail *Email) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	message := []byte("To: " + mail.To + "\r\nSubject: " + mail.Subject + "\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + mail.Text)
	if err := smtp.SendMail(s.address, nil, s.from, []string{mail.To}, message); err != nil {
		return fmt.Errorf("send local verification email: %w", err)
	}
	return nil
}
