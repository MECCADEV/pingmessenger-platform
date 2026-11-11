package httpapi

import (
	"encoding/base64"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/valyala/fasthttp"
	"pingmessenger/internal/ratelimit"
)

func TestModeratorBasicAuthAndConsoleAssets(t *testing.T) {
	api := &API{limiter: ratelimit.NewLocal()}
	if err := api.ConfigureModeratorCredentials("safety-team", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, authorization string) *fasthttp.RequestCtx {
		t.Helper()
		ctx := &fasthttp.RequestCtx{}
		ctx.Request.Header.SetMethod(method)
		ctx.Request.SetRequestURI(path)
		if authorization != "" {
			ctx.Request.Header.Set("Authorization", authorization)
		}
		api.Router()(ctx)
		return ctx
	}
	page := call(http.MethodGet, "/admin", "")
	if page.Response.StatusCode() != http.StatusOK || !strings.Contains(string(page.Response.Body()), "Moderator Console") {
		t.Fatalf("admin page status=%d body=%s", page.Response.StatusCode(), page.Response.Body())
	}
	wrong := "Basic " + base64.StdEncoding.EncodeToString([]byte("safety-team:incorrect"))
	if got := call(http.MethodPost, "/v1/admin/session", wrong); got.Response.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("bad credentials status=%d body=%s", got.Response.StatusCode(), got.Response.Body())
	}
	good := "Basic " + base64.StdEncoding.EncodeToString([]byte("safety-team:correct-horse-battery"))
	if got := call(http.MethodPost, "/v1/admin/session", good); got.Response.StatusCode() != http.StatusOK || !strings.Contains(string(got.Response.Body()), `"role":"moderator"`) {
		t.Fatalf("valid credentials status=%d body=%s", got.Response.StatusCode(), got.Response.Body())
	}
	assets, err := fs.ReadDir(moderatorConsole, "adminui/dist/assets")
	if err != nil || len(assets) == 0 {
		t.Fatalf("console assets missing: %v", err)
	}
	asset := call(http.MethodGet, "/admin/assets/"+assets[0].Name(), "")
	if asset.Response.StatusCode() != http.StatusOK || len(asset.Response.Body()) < 1000 {
		t.Fatalf("bundled JS status=%d size=%d", asset.Response.StatusCode(), len(asset.Response.Body()))
	}
}
