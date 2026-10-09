package notify

import (
	"context"
	"fmt"
	"pingmessenger/internal/config"
)

func New(ctx context.Context, c config.Config) (EmailSender, error) {
	var sender EmailSender
	switch c.EmailProvider {
	case "smtp":
		if c.SMTPAddress == "" || c.SMTPFrom == "" {
			return nil, fmt.Errorf("SMTP_ADDRESS and SMTP_FROM are required")
		}
		sender = NewSMTP(c.SMTPAddress, c.SMTPFrom)
	case "sns":
		var err error
		sender, err = NewSNS(ctx, c.AWSRegion, c.AWSSNSTopicARN)
		if err != nil {
			return nil, err
		}
	case "ses":
		var err error
		sender, err = NewSES(ctx, c.AWSRegion, c.AWSSESFromEmail)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported EMAIL_PROVIDER %q", c.EmailProvider)
	}
	if c.EmailMirrorSMTPAddress != "" {
		return Fanout{Primary: sender, Mirror: NewSMTP(c.EmailMirrorSMTPAddress, c.EmailMirrorFrom)}, nil
	}
	return sender, nil
}
