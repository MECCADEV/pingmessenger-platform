package messages

import (
	"strings"
	"testing"
)

func TestEncodeDecodeStatusReply(t *testing.T) {
	data, err := EncodeCustom(KindStatusReply, StatusReply{ReplyToMessageID: "message-1", Status: "read", UpdatedAtMS: 1})
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeCustom(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != "pingmessenger.status_reply.v1" || got.Type != KindStatusReply || got.Version != 1 || !strings.Contains(string(got.Data), `"reply_to_message_id":"message-1"`) {
		t.Fatalf("unexpected envelope: %+v", got)
	}
}

func TestClientPayloadValidation(t *testing.T) {
	tests := []struct {
		name string
		kind string
		body any
		ok   bool
	}{
		{"location", KindLocation, Location{Latitude: 20.1, Longitude: 73.2}, true},
		{"bad location", KindLocation, Location{Latitude: 91, Longitude: 73.2}, false},
		{"media", KindMedia, Media{MediaType: "video", SourceURL: "https://signed.example/v.mp4", MIMEType: "video/mp4", SizeBytes: 10}, true},
		{"bad media", KindMedia, Media{MediaType: "pdf", SourceURL: "https://signed.example/x", MIMEType: "application/pdf"}, false},
		{"status", KindStatusReply, StatusReply{ReplyToMessageID: "m1", Status: "read", UpdatedAtMS: 1}, true},
		{"payment is server-only", KindPayment, map[string]any{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidatePayload(tt.kind, tt.body); (got == nil) != tt.ok {
				t.Fatalf("ValidatePayload() error=%v, want ok=%v", got, tt.ok)
			}
		})
	}
}

func TestNativeElementsCannotUseCustomEncoder(t *testing.T) {
	if _, err := EncodeCustom(KindAttachment, Attachment{UUID: "d", SourceURL: "https://signed.example/d", FileName: "a.pdf", FileSize: 1}); err == nil {
		t.Fatal("native attachment unexpectedly encoded as custom JSON")
	}
}
