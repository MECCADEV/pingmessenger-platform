package auth

import "testing"

func TestNormalizePlatform(t *testing.T) {
	for input, want := range map[string]string{
		" web ": "web", "iOS": "ios", "ANDROID": "android", "mac": "macos", "linux": "linux",
	} {
		got, ok := NormalizePlatform(input)
		if !ok || got != want {
			t.Fatalf("NormalizePlatform(%q) = %q, %v; want %q, true", input, got, ok, want)
		}
	}
	if _, ok := NormalizePlatform("e2e"); ok {
		t.Fatal("unsupported platform accepted")
	}
}
