package control

import (
	"bytes"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
)

func TestEmbeddedWebConsoleSecurityHeaders(t *testing.T) {
	server := NewServer(Config{ListenAddress: "127.0.0.1:0"}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), nil, nil)
	assetEntries, err := fs.ReadDir(webFiles, "web/dist/assets")
	if err != nil {
		t.Fatal(err)
	}
	requestPaths := []string{"/"}
	var hasJavaScript, hasCSS bool
	for _, entry := range assetEntries {
		if entry.IsDir() {
			continue
		}
		extension := path.Ext(entry.Name())
		hasJavaScript = hasJavaScript || extension == ".js"
		hasCSS = hasCSS || extension == ".css"
		requestPaths = append(requestPaths, "/assets/"+entry.Name())
	}
	if !hasJavaScript || !hasCSS {
		t.Fatalf("Vue build must contain JavaScript and CSS assets: %#v", assetEntries)
	}
	for _, requestPath := range requestPaths {
		request := httptest.NewRequest(http.MethodGet, requestPath, nil)
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Security-Policy"), "object-src 'none'") {
			t.Fatalf("path=%s status=%d CSP=%q", requestPath, response.Code, response.Header().Get("Content-Security-Policy"))
		}
	}
}

func TestEmbeddedVueConsoleUsesExternalAssets(t *testing.T) {
	index, err := webFiles.ReadFile("web/dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(index)
	for _, required := range []string{`id="app"`, `type="module"`, `/assets/`} {
		if !strings.Contains(html, required) {
			t.Errorf("generated Vue index is missing %q", required)
		}
	}
	if strings.Contains(html, "<script>") || strings.Contains(html, "sourceMappingURL") {
		t.Fatal("generated Vue index must not contain inline scripts or source maps")
	}
}
