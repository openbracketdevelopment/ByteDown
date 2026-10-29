package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openbracketdevelopment/ByteDown/internal/config"
	"github.com/openbracketdevelopment/ByteDown/internal/model"
	"github.com/openbracketdevelopment/ByteDown/internal/storage"
	"github.com/openbracketdevelopment/ByteDown/internal/store"
)

type fakeDB struct {
	accepted bool
	conflict bool
	input    model.Input
	image    model.Image
	asset    model.Output
}

func (f *fakeDB) Ping(context.Context) error { return nil }
func (f *fakeDB) Accept(_ context.Context, i, j, h, k string, in model.Input, p model.Profile, n int) (string, string, bool, error) {
	if f.conflict {
		return "", "", false, store.ErrConflict
	}
	f.accepted = true
	f.input = in
	f.image = model.Image{ID: i, JobID: j, Original: in}
	return i, j, false, nil
}
func (f *fakeDB) Job(context.Context, string) (model.Job, error) {
	return model.Job{Status: "queued"}, nil
}
func (f *fakeDB) Image(context.Context, string) (model.Image, error) {
	if f.image.ID == "" {
		return model.Image{}, store.ErrNotFound
	}
	return f.image, nil
}
func (f *fakeDB) List(context.Context, string, int, int) ([]model.Image, error) { return nil, nil }
func (f *fakeDB) Asset(context.Context, string) (model.Output, error)           { return f.asset, nil }
func (f *fakeDB) Delete(context.Context, string) (bool, error)                  { return true, nil }

func setup(t *testing.T, public bool) (http.Handler, *fakeDB, config.Config) {
	t.Helper()
	root := t.TempDir()
	if e := storage.Init(root); e != nil {
		t.Fatal(e)
	}
	c := config.Config{Root: root, APIKey: strings.Repeat("x", 32), PublicVariants: public, Concurrency: 1,
		Limits: model.Limits{UploadBytes: 1024, OutputCount: 16}, Profiles: map[string]model.Profile{
			"web": {Widths: []int{480}, Formats: []string{"webp", "avif"}, IncludeSource: true},
		}}
	db := &fakeDB{}
	validate := func(_ context.Context, path string) (model.Input, error) {
		b, e := os.ReadFile(path)
		if e != nil {
			return model.Input{}, e
		}
		if string(b) != "image bytes" {
			return model.Input{}, errors.New("bad image")
		}
		return model.Input{Format: "png", Width: 20, Height: 10, Bytes: int64(len(b))}, nil
	}
	return New(c, db, validate, func(context.Context) error { return nil }), db, c
}
func request(h http.Handler, method, path string, body io.Reader, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, body)
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAuthenticationAndJSONErrors(t *testing.T) {
	h, _, c := setup(t, false)
	for _, path := range []string{"/v1/images", "/v1/capabilities", "/media/opaque", "/v1/images/id/original"} {
		if w := request(h, "GET", path, nil, ""); w.Code != 401 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	if w := request(h, "GET", "/health/live", nil, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, tc := range []struct {
		method, path string
		code         int
	}{{"GET", "/unknown", 404}, {"PATCH", "/v1/images", 405}, {"GET", "/v1/images?limit=0", 400}, {"GET", "/v1/images?status=unknown", 400}} {
		w := request(h, tc.method, tc.path, nil, c.APIKey)
		var b map[string]any
		if w.Code != tc.code || json.Unmarshal(w.Body.Bytes(), &b) != nil || b["error"] == nil {
			t.Fatalf("invalid error %d %s", w.Code, w.Body.String())
		}
	}
}
func uploadRequest(t *testing.T, h http.Handler, c config.Config, data string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	f, e := writer.CreateFormFile("file", "../../spoof.jpeg")
	if e != nil {
		t.Fatal(e)
	}
	_, _ = io.WriteString(f, data)
	_ = writer.WriteField("profile", "web")
	_ = writer.Close()
	r := httptest.NewRequest("POST", "/v1/images", &body)
	r.ContentLength = -1
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+c.APIKey)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestStreamingUploadPreservesOriginal(t *testing.T) {
	h, db, c := setup(t, false)
	w := uploadRequest(t, h, c, "image bytes")
	if w.Code != 202 || !db.accepted {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	b, e := os.ReadFile(filepath.Join(c.Root, "originals", db.image.ID))
	if e != nil || string(b) != "image bytes" {
		t.Fatal("original not preserved")
	}
	if db.input.Format != "png" {
		t.Fatal("trusted filename instead of validation")
	}
}
func TestUploadLimitsInvalidAndConflict(t *testing.T) {
	for _, tc := range []struct {
		data     string
		conflict bool
		code     int
	}{{strings.Repeat("x", 1025), false, 413}, {"invalid", false, 422}, {"image bytes", true, 409}} {
		h, db, c := setup(t, false)
		db.conflict = tc.conflict
		w := uploadRequest(t, h, c, tc.data)
		if w.Code != tc.code || db.accepted {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		entries, _ := os.ReadDir(filepath.Join(c.Root, "uploads"))
		if len(entries) != 0 {
			t.Fatal("temporary upload leaked")
		}
	}
}
func TestMediaAccessPolicy(t *testing.T) {
	for _, public := range []bool{false, true} {
		h, db, c := setup(t, public)
		db.asset = model.Output{Path: "outputs/asset", Format: "png"}
		if e := os.WriteFile(filepath.Join(c.Root, "outputs", "asset"), []byte("variant"), 0600); e != nil {
			t.Fatal(e)
		}
		w := request(h, "GET", "/media/asset", nil, "")
		if !public && w.Code != 401 {
			t.Fatal("private variant exposed")
		}
		if public && (w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("X-Content-Type-Options") != "nosniff") {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		if w = request(h, "GET", "/v1/images/id/original", nil, ""); w.Code != 401 {
			t.Fatal("original became public")
		}
	}
}
func TestCORS(t *testing.T) {
	h, _, _ := setup(t, false)
	r := httptest.NewRequest("OPTIONS", "/v1/images", nil)
	r.Header.Set("Origin", "https://untrusted.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS allowed by default")
	}

}

func TestDownloadRangeErrorIsJSON(t *testing.T) {
	h, db, c := setup(t, true)
	db.asset = model.Output{Path: "outputs/asset", Format: "png"}
	if e := os.WriteFile(filepath.Join(c.Root, "outputs", "asset"), []byte("variant"), 0600); e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "/media/asset", nil)
	r.Header.Set("Range", "bytes=900-1000")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var body map[string]any
	if w.Code != 416 || w.Header().Get("Content-Type") != "application/json" || json.Unmarshal(w.Body.Bytes(), &body) != nil || body["error"] == nil {
		t.Fatalf("invalid range error: %d %s", w.Code, w.Body.String())
	}
}

func TestExplicitCORSAllowlist(t *testing.T) {
	_, db, c := setup(t, false)
	c.Origins = []string{"https://trusted.example"}
	h := New(c, db, nil, nil)
	for _, origin := range []string{"https://trusted.example", "https://other.example"} {
		r := httptest.NewRequest("OPTIONS", "/v1/images", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if origin == c.Origins[0] {
			if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != origin {
				t.Fatal("configured origin not allowed")
			}
		} else if w.Code != 403 || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("unconfigured origin allowed")
		}
	}
}
