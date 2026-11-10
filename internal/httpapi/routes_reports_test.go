package httpapi

import (
	"testing"

	"pingmessenger/internal/openim"
)

func TestReportMessageMatchesAuthoritativeIdentity(t *testing.T) {
	message := &openim.MessageSnapshot{ClientMsgID: "message-1", SendID: "target-openim", Seq: 42}
	if !reportMessageMatches(message, "message-1", "target-openim", 42) {
		t.Fatal("matching server message rejected")
	}
	for _, tc := range []struct {
		id, sender string
		seq        int64
	}{
		{"forged-id", "target-openim", 42},
		{"message-1", "other-openim", 42},
		{"message-1", "target-openim", 41},
	} {
		if reportMessageMatches(message, tc.id, tc.sender, tc.seq) {
			t.Errorf("mismatched client data accepted: %+v", tc)
		}
	}
}
