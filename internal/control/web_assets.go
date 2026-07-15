package control

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web/*
var webFiles embed.FS

func registerWebAssets(mux *http.ServeMux) {
	root, _ := fs.Sub(webFiles, "web")
	assets := http.FileServer(http.FS(root))
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", secureAssets(assets)))
	mux.HandleFunc("GET /", func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(response, request)
			return
		}
		securityHeaders(response)
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		data, err := webFiles.ReadFile("web/index.html")
		if err != nil {
			http.Error(response, "unavailable", 500)
			return
		}
		_, _ = response.Write(data)
	})
}

func secureAssets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		securityHeaders(response)
		response.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(response, request)
	})
}
func securityHeaders(response http.ResponseWriter) {
	response.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	response.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
}
