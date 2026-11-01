package browser

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/hex"
	"github.com/openbracketdevelopment/ByteDown/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func hash(t *testing.T) string {
	t.Helper()
	salt := make([]byte, 16)
	key, e := pbkdf2.Key(sha256.New, "a secure password", salt, 600000, 32)
	if e != nil {
		t.Fatal(e)
	}
	return "pbkdf2-sha256$600000$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(key)
}
func TestPassword(t *testing.T) {
	h := hash(t)
	if !CheckPassword("a secure password", h) || CheckPassword("wrong", h) || CheckPassword("a secure password", "broken") {
		t.Fatal("password verification failed")
	}
}
func TestSessionAndCSRF(t *testing.T) {
	called := false
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing server-side authorization")
		}
		w.WriteHeader(204)
	})
	h := Wrap(config.Config{UIEnabled: true, OperatorHash: hash(t), APIKey: "secret"}, api)
	login := httptest.NewRequest("POST", "http://example.test/ui/login", strings.NewReader(`{"password":"a secure password"}`))
	login.Header.Set("Origin", "http://evil.test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, login)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	login = httptest.NewRequest("POST", "http://example.test/ui/login", strings.NewReader(`{"password":"a secure password"}`))
	login.Header.Set("Origin", "http://example.test")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, login)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe session cookie")
	}
	req := httptest.NewRequest("GET", "http://example.test/v1/images", nil)
	req.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !called || w.Code != 204 {
		t.Fatal("session did not authorize")
	}
	called = false
	req = httptest.NewRequest("DELETE", "http://example.test/v1/images/id", nil)
	req.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 || called {
		t.Fatal("CSRF bypass")
	}
}
func TestDisabledUI(t *testing.T) {
	h := Wrap(config.Config{}, http.NotFoundHandler())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestReferenceDocumentation(t *testing.T) {
	h := Wrap(config.Config{UIEnabled: true}, http.NotFoundHandler())
	for _, tc := range []struct{ path, mime, content string }{
		{"/api-docs", "text/html", "Your first image."},
		{"/docs-assets/reference.css", "text/css", ".layout"},
		{"/docs-assets/reference.js", "text/javascript", "clipboard.writeText"},
		{"/openapi.yaml", "text/plain", "openapi:"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), tc.mime) || !strings.Contains(w.Body.String(), tc.content) {
			t.Fatal(tc.path, w.Code)
		}
	}
}
