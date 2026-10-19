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
	profileDeleteUserLimit  = 30
	profileDeleteIPLimit    = 60
	friendsListUserLimit    = 60
	friendsListIPLimit      = 120
	blockUserLimit          = 30
	blockIPLimit            = 60
	accountDeleteUserLimit  = 3
	accountDeleteIPLimit    = 10
	documentLinkUserLimit   = 60
	documentLinkIPLimit     = 120
	documentDeleteUserLimit = 60
	documentDeleteIPLimit   = 120
	storyWSUserLimit        = 30
	storyWSIPLimit          = 120
	storyAckUserLimit       = 120
	storyAckIPLimit         = 600
	storyDeleteUserLimit    = 60
	storyDeleteIPLimit      = 120
	storyViewUserLimit      = 120
	storyViewIPLimit        = 600
	storyViewsUserLimit     = 60
	storyViewsIPLimit       = 120
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
	// Check the shared peer bucket first. A rejected abusive peer must not
	// consume a user's bucket, which is especially important behind a NAT.
	for _, item := range []struct {
		key   string
		limit int
	}{
		{key: "pm:rate:" + operation + ":ip:" + ip, limit: ipLimit},
		{key: "pm:rate:" + operation + ":user:" + userID, limit: userLimit},
	} {
		allowed, err := a.limiter.Allow(ctx, item.key, item.limit, rateLimitWindow)
		if err != nil || !allowed {
			return false, err
		}
	}
	return true, nil
}

func (a *API) enforceRate(ctx *fasthttp.RequestCtx, operation, userID string, userLimit, ipLimit int) bool {
	allowed, err := a.allowRateBounded(operation, userID, requestIP(ctx), userLimit, ipLimit)
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

func (a *API) allowRateBounded(operation, userID, ip string, userLimit, ipLimit int) (bool, error) {
	rateCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return a.allowRate(rateCtx, operation, userID, ip, userLimit, ipLimit)
}
