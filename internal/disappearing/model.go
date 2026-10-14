package disappearing

import "time"

type Setting struct {
	ConversationID  string    `json:"conversation_id"`
	UserID          string    `json:"user_id"`
	DurationSeconds int       `json:"duration_seconds"`
	Enabled         bool      `json:"enabled"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Event struct {
	Type            string    `json:"type"`
	ConversationID  string    `json:"conversation_id"`
	UserID          string    `json:"user_id"`
	DurationSeconds int       `json:"duration_seconds"`
	Enabled         bool      `json:"enabled"`
	UpdatedAt       time.Time `json:"updated_at"`
}
