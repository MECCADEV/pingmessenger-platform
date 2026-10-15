package httpapi

import (
	"context"
	"log"
	"time"

	"pingmessenger/internal/documents"
	"pingmessenger/internal/openim"
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
	links, err := a.documents.ClaimExpiredMessageLinks(ctx, 100)
	if err != nil {
		log.Printf("message media cleanup query failed: %v", err)
	} else {
		for _, link := range links {
			if !link.Revoked {
				if a.openIM == nil {
					_ = a.documents.RecordMessageCleanupError(ctx, link.ID, "OpenIM client is unavailable")
					continue
				}
				if err := a.openIM.RevokeMessage(ctx, &openim.RevokeMessageRequest{ConversationID: link.ConversationID, Seq: link.Seq, UserID: link.OpenIMUserID}); err != nil {
					_ = a.documents.RecordMessageCleanupError(ctx, link.ID, err.Error())
					log.Printf("OpenIM message revoke failed for media link %s: %v", link.ID, err)
					continue
				}
				if err := a.documents.MarkMessageRevoked(ctx, link.ID); err != nil {
					log.Printf("message revoke marker failed for media link %s: %v", link.ID, err)
					continue
				}
			}
			if !link.S3Deleted {
				if err := a.deleteDocumentObject(ctx, documents.CleanupItem{ID: link.DocumentID, StorageKey: link.StorageKey}); err != nil {
					log.Printf("linked S3 cleanup failed for media link %s: %v", link.ID, err)
					continue
				}
				if err := a.documents.MarkMessageS3Deleted(ctx, link.ID); err != nil {
					log.Printf("linked S3 marker failed for media link %s: %v", link.ID, err)
				}
			}
		}
	}
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
