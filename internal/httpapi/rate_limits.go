package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/valyala/fasthttp"
)

const (
	storyUploadUserLimit    = 10
	storyUploadIPLimit      = 30
	documentUploadUserLimit = 10
	documentUploadIPLimit   = 30
	profileUploadUserLimit  = 10
	profileUploadIPLimit    = 30
	signedURLUserLimit      = 60
	signedURLIPLimit        = 300
	downloadUserLimit       = 120
	downloadIPLimit         = 600
	rateLimitWindow         = time.Minute
)

func (a *API) allowRate(ctx context.Context, operation, userID, ip string, userLimit, ipLimit int) (bool, error) {
	if a.limiter == nil {
		return true, nil
	}
	for _, item := range []struct {
		key   string
		limit int
	}{
		{key: "pm:rate:" + operation + ":user:" + userID, limit: userLimit},
		{key: "pm:rate:" + operation + ":ip:" + ip, limit: ipLimit},
	} {
		allowed, err := a.limiter.Allow(ctx, item.key, item.limit, rateLimitWindow)
		if err != nil || !allowed {
			return false, err
		}
	}
	return true, nil
}

func (a *API) enforceRate(ctx *fasthttp.RequestCtx, operation, userID string, userLimit, ipLimit int) bool {
	allowed, err := a.allowRate(context.Background(), operation, userID, requestIP(ctx), userLimit, ipLimit)
	if err != nil {
		writeJSON(ctx, http.StatusServiceUnavailable, map[string]string{"error": "rate limit service unavailable"})
		return false
	}
	if !allowed {
		ctx.Response.Header.Set("Retry-After", strconv.Itoa(int(rateLimitWindow.Seconds())))
		writeJSON(ctx, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return false
	}
	return true
}
