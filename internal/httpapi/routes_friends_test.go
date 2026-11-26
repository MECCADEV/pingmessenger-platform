package httpapi

import (
	"context"
	"testing"

	"pingmessenger/internal/openim"
)

type blockStatusBatchClient struct {
	openim.Client
	statuses map[string]bool
}

func (c blockStatusBatchClient) BlockedBy(_ context.Context, _ string, _ []string) (map[string]bool, error) {
	return c.statuses, nil
}

func TestBlockedByRequesterPreservesFalseBatchResults(t *testing.T) {
	api := &API{openIM: blockStatusBatchClient{statuses: map[string]bool{
		"blocked-target": true,
		"available-user": false,
	}}}

	got, err := api.blockedByRequester(context.Background(), "viewer", []string{"blocked-target", "available-user"})
	if err != nil {
		t.Fatalf("blockedByRequester() error = %v", err)
	}
	if !got["blocked-target"] {
		t.Fatalf("blocked target missing from result: %#v", got)
	}
	if got["available-user"] {
		t.Fatalf("available user was marked blocked: %#v", got)
	}
}
