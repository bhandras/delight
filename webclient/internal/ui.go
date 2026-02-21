package webclient

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// staticFiles embeds the web client SPA assets.
//
//go:embed static/*
var staticFiles embed.FS

// handleUI serves static web assets and SPA index fallback.
func (a *App) handleUI(w http.ResponseWriter, r *http.Request) {
	requested := strings.TrimSpace(r.URL.Path)
	if requested == "" || requested == "/" {
		requested = "/index.html"
	}
	clean := path.Clean(requested)
	clean = strings.TrimPrefix(clean, "/")
	if !strings.HasPrefix(clean, "static/") {
		clean = path.Join("static", clean)
	}
	content, err := fs.ReadFile(staticFiles, clean)
	if err != nil {
		indexContent, indexErr := fs.ReadFile(staticFiles, "static/index.html")
		if indexErr != nil {
			http.Error(w, "index not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexContent)
		return
	}
	w.Header().Set("Content-Type", detectContentType(clean))
	_, _ = w.Write(content)
}

// detectContentType returns content type based on file extension.
func detectContentType(fileName string) string {
	switch {
	case strings.HasSuffix(fileName, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(fileName, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(fileName, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(fileName, ".json"):
		return "application/json; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
