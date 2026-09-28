// Package notify provides the outbound verification-message boundary.
package notify

import "context"

type Email struct{ To, Subject, Text string }
type EmailSender interface {
	Send(context.Context, *Email) error
}
