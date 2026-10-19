package stories

import "time"

const (
	MaxActiveStories = 10
	MaxTotalStories  = 40
)

type Visibility string

const (
	VisibilityContacts Visibility = "contacts"
	VisibilityAllow    Visibility = "allow"
	VisibilityExclude  Visibility = "exclude"
)

type MediaType string

const (
	MediaImage MediaType = "image"
	MediaVideo MediaType = "video"
	MediaText  MediaType = "text"
)

type Story struct {
	ID         string    `json:"id"`
	OwnerID    string    `json:"owner_id"`
	StorageKey string    `json:"-"`
	MediaType  MediaType `json:"media_type"`
	// MediaURL is populated only at the HTTP/WebSocket boundary with a
	// short-lived presigned URL. It is not an authoritative persisted field.
	MediaURL        string     `json:"media_url"`
	ContentType     string     `json:"content_type"`
	ByteSize        int64      `json:"byte_size"`
	Caption         string     `json:"caption"`
	Text            string     `json:"text,omitempty"`
	BackgroundColor string     `json:"background_color,omitempty"`
	Visibility      Visibility `json:"visibility"`
	ExpiresAt       time.Time  `json:"expires_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type Event struct {
	EventID   int64      `json:"event_id"`
	Type      string     `json:"type"`
	Story     *Story     `json:"story,omitempty"`
	StoryID   string     `json:"story_id"`
	OwnerID   string     `json:"owner_id"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type View struct {
	UserID   string    `json:"user_id"`
	ViewedAt time.Time `json:"viewed_at"`
	Reaction string    `json:"reaction"`
}

type SyncResult struct {
	Events       []*Event         `json:"events"`
	GlobalCursor int64            `json:"global_cursor"`
	UserCursors  map[string]int64 `json:"user_cursors"`
	HasMore      bool             `json:"has_more"`
}
