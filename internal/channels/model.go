package channels

import "time"

type Visibility string

const (
	VisibilityPublic  Visibility = "public"
	VisibilityPrivate Visibility = "private"
)

type Channel struct {
	ID            string     `json:"id"`
	OpenIMGroupID string     `json:"openim_group_id"`
	CreatedBy     string     `json:"created_by"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	PhotoURL      string     `json:"photo_url"`
	Visibility    Visibility `json:"visibility"`
	MemberCount   int        `json:"member_count"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type Pin struct {
	ChannelID string    `json:"channel_id"`
	MessageID string    `json:"message_id"`
	PinnedBy  string    `json:"pinned_by"`
	PinnedAt  time.Time `json:"pinned_at"`
}

type CreateParams struct {
	OpenIMGroupID string
	CreatedBy     string
	Name          string
	Description   string
	PhotoURL      string
	Visibility    Visibility
}
