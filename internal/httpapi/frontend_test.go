package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFrontendIsServedFromSameOrigin(t *testing.T) {
	handler := New(nil, nil)

	tests := []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/", contentType: "text/html; charset=utf-8", contains: `id="overview-page"`},
		{path: "/assets/app.js", contentType: "text/javascript; charset=utf-8", contains: "refreshDashboard"},
		{path: "/assets/styles.css", contentType: "text/css; charset=utf-8", contains: "@media (max-width: 780px)"},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			if got := response.Header().Get("Content-Type"); got != test.contentType {
				t.Fatalf("Content-Type = %q, want %q", got, test.contentType)
			}
			body, err := io.ReadAll(response.Result().Body)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			if !strings.Contains(string(body), test.contains) {
				t.Fatalf("response does not contain %q", test.contains)
			}
		})
	}
}
