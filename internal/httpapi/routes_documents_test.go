package httpapi

import "testing"

func TestSafeDocumentName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"report.pdf", "report.pdf"},
		{"nested/report.pdf", "report.pdf"},
		{"", ""},
		{"../secrets", "secrets"},
		{"bad\nname.pdf", ""},
	}
	for _, tt := range tests {
		if got := safeDocumentName(tt.name); got != tt.want {
			t.Errorf("safeDocumentName(%q)=%q, want %q", tt.name, got, tt.want)
		}
	}
}
