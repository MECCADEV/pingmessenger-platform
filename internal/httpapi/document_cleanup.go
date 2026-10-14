package httpapi

import (
	"context"
	"log"
	"time"
)

// RunDocumentCleanup expires metadata and retries S3 deletion. The document
// row remains as a tombstone so OpenIM message history can outlive the object.
func (a *API) RunDocumentCleanup(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		a.cleanupDocuments(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *API) cleanupDocuments(ctx context.Context) {
	expired, err := a.documents.ClaimExpired(ctx, 100)
	if err != nil {
		log.Printf("document expiry query failed: %v", err)
		return
	}
	pending, err := a.documents.PendingCleanup(ctx, 100)
	if err != nil {
		log.Printf("document cleanup query failed: %v", err)
		return
	}
	items := append(expired, pending...)
	for _, item := range items {
		if err := a.deleteDocumentObject(ctx, item); err != nil {
			log.Printf("document S3 cleanup failed for %s: %v", item.ID, err)
		}
	}
}
