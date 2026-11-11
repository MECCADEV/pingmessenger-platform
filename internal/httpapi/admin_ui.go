package httpapi

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/valyala/fasthttp"
)

//go:embed adminui/dist
var moderatorConsole embed.FS

func (a *API) serveModeratorUI(ctx *fasthttp.RequestCtx) {
	if string(ctx.Method()) != http.MethodGet && string(ctx.Method()) != http.MethodHead {
		ctx.SetStatusCode(http.StatusMethodNotAllowed)
		return
	}
	requested := strings.TrimPrefix(string(ctx.Path()), "/admin")
	name := strings.TrimPrefix(path.Clean("/"+requested), "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	asset, err := fs.ReadFile(moderatorConsole, "adminui/dist/"+name)
	if err != nil {
		ctx.SetStatusCode(http.StatusNotFound)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	ctx.SetContentType(contentType)
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	ctx.Response.Header.Set("X-Frame-Options", "DENY")
	ctx.Response.Header.Set("Referrer-Policy", "same-origin")
	ctx.Response.Header.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
	if name == "index.html" {
		ctx.Response.Header.Set("Cache-Control", "no-cache")
	} else {
		ctx.Response.Header.Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	if string(ctx.Method()) == http.MethodHead {
		ctx.SetStatusCode(http.StatusOK)
		ctx.Response.Header.SetContentLength(len(asset))
		return
	}
	ctx.SetStatusCode(http.StatusOK)
	_, _ = ctx.Write(asset)
}
