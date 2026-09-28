package notify

import (
	"context"
	"fmt"
	"pingmessenger/internal/config"
)

func New(ctx context.Context, c config.Config) (EmailSender, error) {
	switch c.EmailProvider {
	case "smtp":
		if c.SMTPAddress == "" || c.SMTPFrom == "" {
			return nil, fmt.Errorf("SMTP_ADDRESS and SMTP_FROM are required")
		}
		return NewSMTP(c.SMTPAddress, c.SMTPFrom), nil
	case "sns":
		return NewSNS(ctx, c.AWSRegion, c.AWSSNSTopicARN)
	default:
		return nil, fmt.Errorf("unsupported EMAIL_PROVIDER %q", c.EmailProvider)
	}
}
