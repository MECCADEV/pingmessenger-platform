package documents

import (
	"testing"
	"time"
)

func TestNewPolicyParsesLimitsAndMIMEs(t *testing.T) {
	p := NewPolicy(100, 2, 300, 7*24*time.Hour, "application/pdf, text/plain")
	if p.MaxBytes != 100 || p.MaxCount != 2 || p.MaxBytes24h != 300 || p.Retention != 7*24*time.Hour {
		t.Fatalf("unexpected policy: %+v", p)
	}
	if _, ok := p.AllowedMIMEs["application/pdf"]; !ok {
		t.Fatal("expected normalized PDF MIME")
	}
	if _, ok := p.AllowedMIMEs["text/plain"]; !ok {
		t.Fatal("expected trimmed text MIME")
	}
}
