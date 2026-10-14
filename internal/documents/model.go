package documents

import (
	"strings"
	"time"
)

type Policy struct {
	MaxBytes     int64
	MaxCount     int
	MaxBytes24h  int64
	Retention    time.Duration
	AllowedMIMEs map[string]struct{}
}

func NewPolicy(maxBytes int64, maxCount int, maxBytes24h int64, retention time.Duration, allowed string) Policy {
	set := make(map[string]struct{})
	for _, raw := range strings.Split(allowed, ",") {
		if v := strings.ToLower(strings.TrimSpace(raw)); v != "" {
			set[v] = struct{}{}
		}
	}
	return Policy{MaxBytes: maxBytes, MaxCount: maxCount, MaxBytes24h: maxBytes24h, Retention: retention, AllowedMIMEs: set}
}

func DefaultPolicy() Policy {
	return NewPolicy(100<<20, 20, 1<<30, 7*24*time.Hour, "application/pdf,application/rtf,application/msword,application/vnd.openxmlformats-officedocument.wordprocessingml.document,application/vnd.ms-excel,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet,application/vnd.ms-powerpoint,application/vnd.openxmlformats-officedocument.presentationml.presentation,text/plain,text/csv,application/zip")
}

type Document struct {
	ID          string     `json:"id"`
	OwnerID     string     `json:"owner_id"`
	StorageKey  string     `json:"-"`
	FileName    string     `json:"file_name"`
	ContentType string     `json:"content_type"`
	ByteSize    int64      `json:"file_size"`
	SourceURL   string     `json:"source_url,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

type CleanupItem struct {
	ID         string
	StorageKey string
}
