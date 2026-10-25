package httpapi

import (
	"testing"

	"github.com/valyala/fasthttp"
	"pingmessenger/internal/notifications"
)

func TestValidateNotificationInput(t *testing.T) {
	frequency := 5
	mode := "mentions_only"
	invalid := "loud"
	if err := validateNotificationInput(notifications.Input{Mode: &mode, MaxUpdateFrequencySeconds: &frequency}); err != nil {
		t.Fatalf("valid partial update rejected: %v", err)
	}
	for _, input := range []notifications.Input{
		{},
		{Sound: &invalid},
		{MaxUpdateFrequencySeconds: intPointer(3)},
	} {
		if err := validateNotificationInput(input); err == nil {
			t.Errorf("invalid preference accepted: %+v", input)
		}
	}
}

func TestNotificationResolverRequiresDedicatedServiceToken(t *testing.T) {
	t.Run("unconfigured", func(t *testing.T) {
		api := &API{}
		ctx := new(fasthttp.RequestCtx)
		api.resolveNotificationPreferences(ctx)
		if got := ctx.Response.StatusCode(); got != fasthttp.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", got)
		}
	})
	t.Run("wrong token", func(t *testing.T) {
		api := &API{openIMServiceToken: "configured-service-secret"}
		ctx := new(fasthttp.RequestCtx)
		ctx.Request.Header.Set("X-OpenIM-Service-Token", "wrong")
		api.resolveNotificationPreferences(ctx)
		if got := ctx.Response.StatusCode(); got != fasthttp.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", got)
		}
	})
}

func intPointer(value int) *int { return &value }
