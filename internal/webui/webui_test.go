package webui

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The repo checkout has no built UI (dist holds only .gitkeep), so the
// handler must fall back to the placeholder for every path.
func TestPlaceholderWhenNotBuilt(t *testing.T) {
	if Present() {
		t.Skip("UI is built into this checkout")
	}
	h := Handler()
	for _, path := range []string{"/", "/accounts", "/assets/missing.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "not included in this build") {
			t.Fatalf("%s: expected placeholder page, got %q", path, rec.Body.String()[:min(120, rec.Body.Len())])
		}
	}
}
