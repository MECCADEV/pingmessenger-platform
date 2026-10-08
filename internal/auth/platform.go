package auth

import "strings"

// NormalizePlatform accepts only platforms that can later receive an OpenIM
// device token. Keeping this at login prevents unusable session records.
func NormalizePlatform(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ios":
		return "ios", true
	case "android":
		return "android", true
	case "windows":
		return "windows", true
	case "mac", "macos":
		return "macos", true
	case "web":
		return "web", true
	case "linux":
		return "linux", true
	default:
		return "", false
	}
}
