// Package notify provides the outbound verification-message boundary.
package notify

import "context"

type Email struct{ To, Subject, Text string }
type EmailSender interface {
	Send(context.Context, *Email) error
}

// Fanout is an explicitly configured test/operations mirror. The primary
// sender remains authoritative; mirror failures never make a real inbox send
// appear failed. Keep EMAIL_MIRROR_SMTP_ADDRESS unset in production.
type Fanout struct {
	Primary EmailSender
	Mirror  EmailSender
}

func (f Fanout) Send(ctx context.Context, mail *Email) error {
	if err := f.Primary.Send(ctx, mail); err != nil {
		return err
	}
	if f.Mirror != nil {
		_ = f.Mirror.Send(ctx, mail)
	}
	return nil
}

type SMS struct{ To, Text string }
type SMSSender interface {
	SendSMS(context.Context, *SMS) error
}
