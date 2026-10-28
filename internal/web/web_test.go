package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServesPageAndAssets(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)

	cases := []struct {
		path, contains, contentType string
		code                        int
	}{
		{"/", "<title>Catalog Updates</title>", "text/html", http.StatusOK},
		{"/static/app.js", "/api/updates", "javascript", http.StatusOK},
		{"/static/style.css", "--accent", "text/css", http.StatusOK},
		{"/nope", "", "", http.StatusNotFound},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", c.path, nil))
		body, _ := io.ReadAll(rec.Body)
		if rec.Code != c.code {
			t.Errorf("%s: code %d, want %d", c.path, rec.Code, c.code)
			continue
		}
		if !strings.Contains(string(body), c.contains) {
			t.Errorf("%s: body does not contain %q", c.path, c.contains)
		}
		if !strings.Contains(rec.Header().Get("Content-Type"), c.contentType) {
			t.Errorf("%s: content type %q", c.path, rec.Header().Get("Content-Type"))
		}
	}
}
