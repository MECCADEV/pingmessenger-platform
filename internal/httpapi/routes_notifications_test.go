package httpapi

import (
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
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

func TestNotificationResolverRejectsRecipientIDLongerThanContractLimit(t *testing.T) {
	api := &API{openIMServiceToken: "configured-service-secret", validate: validator.New()}
	ctx := new(fasthttp.RequestCtx)
	ctx.Request.Header.Set("X-OpenIM-Service-Token", "configured-service-secret")
	ctx.Request.Header.SetContentType("application/json")
	ctx.Request.SetBodyString(`{"openim_user_ids":["` + strings.Repeat("x", 129) + `"],"conversation_id":"conversation-1"}`)

	api.resolveNotificationPreferences(ctx)
	if got := ctx.Response.StatusCode(); got != fasthttp.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", got)
	}
}

func TestNotificationResolverIDLimitsCountUnicodeCharacters(t *testing.T) {
	validID := strings.Repeat("界", 128)
	if normalized, ok := normalizeOpenIMUserID(validID); !ok || normalized != validID {
		t.Fatal("128-character UTF-8 recipient ID should be accepted")
	}
	if _, ok := normalizeOpenIMUserID(validID + "界"); ok {
		t.Fatal("129-character UTF-8 recipient ID should be rejected")
	}
	if !validConversationID(strings.Repeat("界", 256)) {
		t.Fatal("256-character UTF-8 conversation ID should be accepted")
	}
	if validConversationID(strings.Repeat("界", 257)) {
		t.Fatal("257-character UTF-8 conversation ID should be rejected")
	}
}

func intPointer(value int) *int { return &value }
