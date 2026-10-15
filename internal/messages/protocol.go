// Package messages defines the PingMessenger message payload contract.
//
// OpenIM transports the payloads; it does not interpret the PingMessenger
// schemas. Native OpenIM element messages are kept for SDK compatibility,
// while custom JSON messages use contentType=110 and a versioned envelope.
package messages

import (
	"errors"
	"fmt"
	"math"
	"strings"

	json "github.com/goccy/go-json"
)

const (
	// OpenIM content types used by this contract.
	ContentTypeVoice    int32 = 103
	ContentTypePicture  int32 = 102
	ContentTypeVideo    int32 = 104
	ContentTypeFile     int32 = 105
	ContentTypeCard     int32 = 108
	ContentTypeLocation int32 = 109
	ContentTypeCustom   int32 = 110

	KindContact     = "contact"
	KindLocation    = "location"
	KindAttachment  = "attachment"
	KindImage       = "image"
	KindVideo       = "video"
	KindMedia       = "media"
	KindStatusReply = "status_reply"
	KindPayment     = "payment"
)

var (
	ErrInvalidMessage = errors.New("invalid message payload")
	ErrServerOnly     = errors.New("message is server-created")
	ErrNativeElement  = errors.New("message uses a native OpenIM element")
	ErrUnknownMessage = errors.New("unknown message kind")
)

// Definition describes the wire format and producer for one message kind.
// Producer is deliberately part of the public contract so clients cannot
// accidentally forge a payment projection or other platform-owned message.
type Definition struct {
	Kind        string
	Schema      string
	ContentType int32
	Encoding    string // custom-json or openim-native
	Producer    string // client or server
}

var definitions = map[string]Definition{
	KindContact:     {Kind: KindContact, Schema: "pingmessenger.contact.v1", ContentType: ContentTypeCard, Encoding: "openim-native", Producer: "client"},
	KindLocation:    {Kind: KindLocation, Schema: "pingmessenger.location.v1", ContentType: ContentTypeLocation, Encoding: "openim-native", Producer: "client"},
	KindAttachment:  {Kind: KindAttachment, Schema: "pingmessenger.attachment.v1", ContentType: ContentTypeFile, Encoding: "openim-native", Producer: "client"},
	KindImage:       {Kind: KindImage, Schema: "pingmessenger.image.v1", ContentType: ContentTypePicture, Encoding: "openim-native", Producer: "client"},
	KindVideo:       {Kind: KindVideo, Schema: "pingmessenger.video.v1", ContentType: ContentTypeVideo, Encoding: "openim-native", Producer: "client"},
	KindMedia:       {Kind: KindMedia, Schema: "pingmessenger.media.v1", ContentType: ContentTypeCustom, Encoding: "custom-json", Producer: "client"},
	KindStatusReply: {Kind: KindStatusReply, Schema: "pingmessenger.status_reply.v1", ContentType: ContentTypeCustom, Encoding: "custom-json", Producer: "client"},
	KindPayment:     {Kind: KindPayment, Schema: "pingmessenger.payment.v1", ContentType: ContentTypeCustom, Encoding: "custom-json", Producer: "server"},
}

// DefinitionFor returns a copy of the immutable message definition.
func DefinitionFor(kind string) (Definition, bool) {
	d, ok := definitions[kind]
	return d, ok
}

// Envelope is the common wire shape for custom JSON messages. Data is kept as
// RawMessage so clients can decode it according to Type without losing fields.
type Envelope struct {
	Schema  string          `json:"schema"`
	Type    string          `json:"type"`
	Version int             `json:"version"`
	Data    json.RawMessage `json:"data"`
}

type Contact struct {
	UserID   string `json:"userID"`
	Nickname string `json:"nickname"`
	FaceURL  string `json:"faceURL"`
	Ex       string `json:"ex"`
}

type Location struct {
	Description string  `json:"description,omitempty"`
	Longitude   float64 `json:"longitude"`
	Latitude    float64 `json:"latitude"`
}

// Attachment is the metadata returned by the authenticated document upload.
// SourceURL is a short-lived signed URL and must never be treated as a public
// permanent object key.
type Attachment struct {
	FilePath  string `json:"filePath,omitempty"`
	UUID      string `json:"uuid"`
	SourceURL string `json:"sourceUrl"`
	FileName  string `json:"fileName"`
	FileSize  int64  `json:"fileSize"`
	FileType  string `json:"fileType,omitempty"`
}

type PictureBaseInfo struct {
	UUID   string `json:"uuid,omitempty"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	Width  int32  `json:"width"`
	Height int32  `json:"height"`
	URL    string `json:"url"`
}

type PictureElem struct {
	SourcePath      string           `json:"sourcePath,omitempty"`
	SourcePicture   *PictureBaseInfo `json:"sourcePicture"`
	BigPicture      *PictureBaseInfo `json:"bigPicture"`
	SnapshotPicture *PictureBaseInfo `json:"snapshotPicture"`
}

type VideoElem struct {
	VideoPath      string `json:"videoPath,omitempty"`
	VideoUUID      string `json:"videoUUID,omitempty"`
	VideoURL       string `json:"videoUrl"`
	VideoType      string `json:"videoType"`
	VideoSize      int64  `json:"videoSize"`
	Duration       int64  `json:"duration"`
	SnapshotPath   string `json:"snapshotPath,omitempty"`
	SnapshotUUID   string `json:"snapshotUUID,omitempty"`
	SnapshotSize   int64  `json:"snapshotSize"`
	SnapshotURL    string `json:"snapshotUrl"`
	SnapshotWidth  int32  `json:"snapshotWidth"`
	SnapshotHeight int32  `json:"snapshotHeight"`
	SnapshotType   string `json:"snapshotType,omitempty"`
}

type SoundElem struct {
	UUID      string `json:"uuid,omitempty"`
	SoundPath string `json:"soundPath,omitempty"`
	SourceURL string `json:"sourceUrl"`
	DataSize  int64  `json:"dataSize"`
	Duration  int64  `json:"duration"`
	SoundType string `json:"soundType,omitempty"`
}

type Media struct {
	MediaType    string `json:"media_type"` // image, video, or audio
	SourceURL    string `json:"source_url"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	MIMEType     string `json:"mime_type"`
	SizeBytes    int64  `json:"size_bytes"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	DurationMS   int64  `json:"duration_ms,omitempty"`
}

type StatusReply struct {
	ReplyToMessageID string `json:"reply_to_message_id"`
	Status           string `json:"status"` // sent, delivered, read, failed, edited, revoked
	Text             string `json:"text,omitempty"`
	UpdatedAtMS      int64  `json:"updated_at_ms"`
}

// ValidatePayload validates a client-created payload before it is sent to
// OpenIM. Payment payloads are intentionally rejected here: only the platform
// ledger projection may create pingmessenger.payment.v1 messages.
func ValidatePayload(kind string, payload any) error {
	d, ok := DefinitionFor(kind)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownMessage, kind)
	}
	if d.Producer != "client" {
		return fmt.Errorf("%w: %s", ErrServerOnly, kind)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	switch kind {
	case KindContact:
		var v Contact
		if err := json.Unmarshal(raw, &v); err != nil || strings.TrimSpace(v.UserID) == "" {
			return fmt.Errorf("%w: contact.user_id is required", ErrInvalidMessage)
		}
	case KindLocation:
		var v Location
		if err := json.Unmarshal(raw, &v); err != nil || math.IsNaN(v.Latitude) || math.IsInf(v.Latitude, 0) || math.IsNaN(v.Longitude) || math.IsInf(v.Longitude, 0) || v.Latitude < -90 || v.Latitude > 90 || v.Longitude < -180 || v.Longitude > 180 {
			return fmt.Errorf("%w: location coordinates are invalid", ErrInvalidMessage)
		}
	case KindAttachment:
		var v Attachment
		if err := json.Unmarshal(raw, &v); err != nil || strings.TrimSpace(v.UUID) == "" || strings.TrimSpace(v.SourceURL) == "" || strings.TrimSpace(v.FileName) == "" || v.FileSize <= 0 {
			return fmt.Errorf("%w: attachment fields are invalid", ErrInvalidMessage)
		}
	case KindImage:
		var v PictureElem
		if err := json.Unmarshal(raw, &v); err != nil || !validPicture(v.SourcePicture) || !validPicture(v.BigPicture) || !validPicture(v.SnapshotPicture) {
			return fmt.Errorf("%w: picture element fields are invalid", ErrInvalidMessage)
		}
	case KindVideo:
		var v VideoElem
		if err := json.Unmarshal(raw, &v); err != nil || strings.TrimSpace(v.VideoURL) == "" || strings.TrimSpace(v.VideoType) == "" || v.VideoSize <= 0 || v.Duration <= 0 || strings.TrimSpace(v.SnapshotURL) == "" || v.SnapshotWidth <= 0 || v.SnapshotHeight <= 0 {
			return fmt.Errorf("%w: video element fields are invalid", ErrInvalidMessage)
		}
	case KindMedia:
		var v Media
		if err := json.Unmarshal(raw, &v); err != nil || strings.TrimSpace(v.SourceURL) == "" || strings.TrimSpace(v.MIMEType) == "" || v.SizeBytes < 0 || v.Width < 0 || v.Height < 0 || v.DurationMS < 0 {
			return fmt.Errorf("%w: media fields are invalid", ErrInvalidMessage)
		}
		if kind == KindMedia && v.MediaType != "image" && v.MediaType != "video" && v.MediaType != "audio" {
			return fmt.Errorf("%w: media_type must be image, video, or audio", ErrInvalidMessage)
		}
	case KindStatusReply:
		var v StatusReply
		if err := json.Unmarshal(raw, &v); err != nil || strings.TrimSpace(v.ReplyToMessageID) == "" || !validStatus(v.Status) || v.UpdatedAtMS <= 0 {
			return fmt.Errorf("%w: status reply fields are invalid", ErrInvalidMessage)
		}
	default:
		return fmt.Errorf("%w: %s", ErrUnknownMessage, kind)
	}
	return nil
}

func validPicture(v *PictureBaseInfo) bool {
	return v != nil && strings.TrimSpace(v.Type) != "" && v.Size > 0 && v.Width > 0 && v.Height > 0 && strings.TrimSpace(v.URL) != ""
}

func validStatus(status string) bool {
	switch status {
	case "sent", "delivered", "read", "failed", "edited", "revoked":
		return true
	default:
		return false
	}
}

// EncodeCustom creates a validated client-created custom message. Native
// OpenIM elements must be sent using the SDK's native element structs; use
// DefinitionFor to select contentType=105/102/104 for those messages.
func EncodeCustom(kind string, payload any) ([]byte, error) {
	d, ok := DefinitionFor(kind)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownMessage, kind)
	}
	if d.Encoding != "custom-json" {
		return nil, fmt.Errorf("%w: %s", ErrNativeElement, kind)
	}
	if err := ValidatePayload(kind, payload); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", kind, err)
	}
	return json.Marshal(Envelope{Schema: d.Schema, Type: kind, Version: 1, Data: raw})
}

func DecodeCustom(data []byte) (Envelope, error) {
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Envelope{}, fmt.Errorf("decode custom message: %w", err)
	}
	d, ok := DefinitionFor(envelope.Type)
	if !ok || d.Encoding != "custom-json" {
		return Envelope{}, fmt.Errorf("%w: %s", ErrUnknownMessage, envelope.Type)
	}
	if d.Producer != "client" {
		return Envelope{}, fmt.Errorf("%w: %s", ErrServerOnly, envelope.Type)
	}
	if envelope.Schema != d.Schema || envelope.Version != 1 || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return Envelope{}, fmt.Errorf("%w: custom envelope metadata is invalid", ErrInvalidMessage)
	}
	if err := ValidatePayload(envelope.Type, envelope.Data); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}
