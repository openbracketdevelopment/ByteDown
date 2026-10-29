package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/openbracketdevelopment/ByteDown/internal/auth"
	"github.com/openbracketdevelopment/ByteDown/internal/config"
	"github.com/openbracketdevelopment/ByteDown/internal/model"
	"github.com/openbracketdevelopment/ByteDown/internal/processing"
	"github.com/openbracketdevelopment/ByteDown/internal/storage"
	"github.com/openbracketdevelopment/ByteDown/internal/store"
)

type Database interface {
	Ping(context.Context) error
	Accept(context.Context, string, string, string, string, model.Input, model.Profile, int) (string, string, bool, error)
	Job(context.Context, string) (model.Job, error)
	Image(context.Context, string) (model.Image, error)
	List(context.Context, string, int, int) ([]model.Image, error)
	Asset(context.Context, string) (model.Output, error)
	Delete(context.Context, string) (bool, error)
}

type Server struct {
	c               config.Config
	db              Database
	validate        func(context.Context, string) (model.Input, error)
	probe           func(context.Context) error
	validationSlots chan struct{}
}

func New(c config.Config, db Database, validate func(context.Context, string) (model.Input, error), probe func(context.Context) error) http.Handler {
	s := &Server{c: c, db: db, validate: validate, probe: probe, validationSlots: make(chan struct{}, c.Concurrency)}
	m := http.NewServeMux()
	m.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]string{"status": "ok"}) })
	m.HandleFunc("GET /health/ready", s.ready)
	m.HandleFunc("GET /v1/capabilities", s.capabilities)
	m.HandleFunc("POST /v1/images", s.upload)
	m.HandleFunc("GET /v1/jobs/{id}", s.job)
	m.HandleFunc("GET /v1/images", s.list)
	m.HandleFunc("GET /v1/images/{id}", s.image)
	m.HandleFunc("GET /v1/images/{id}/original", s.original)
	m.HandleFunc("DELETE /v1/images/{id}", s.delete)
	m.HandleFunc("GET /media/{id}", s.asset)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timeout := c.Limits.Duration + 30*time.Second
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		r = r.WithContext(ctx)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		origin := r.Header.Get("Origin")
		if origin != "" {
			allowed := false
			for _, o := range c.Origins {
				if origin == o {
					allowed = true
				}
			}
			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
				if r.Method == "OPTIONS" {
					w.WriteHeader(204)
					return
				}
			} else if r.Method == "OPTIONS" {
				fail(w, 403, "cors_denied", "Origin is not allowed")
				return
			}
		}
		public := r.URL.Path == "/health/live" || r.URL.Path == "/health/ready" || (c.PublicVariants && strings.HasPrefix(r.URL.Path, "/media/"))
		if !public && !auth.Valid(r, c.APIKey) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			fail(w, 401, "unauthorized", "A valid bearer API key is required")
			return
		}
		// ServeMux's default errors are plain text. Resolve routes here to keep
		// every API error in the same JSON envelope.
		_, pattern := m.Handler(r)
		if pattern == "" {
			status := 404
			for _, method := range []string{"GET", "POST", "DELETE"} {
				clone := r.Clone(r.Context())
				clone.Method = method
				if _, p := m.Handler(clone); p != "" {
					status = 405
					break
				}
			}
			fail(w, status, "route_not_found", "No matching endpoint or method")
			return
		}
		m.ServeHTTP(w, r)
	})
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code, message string) {
	write(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func lookupError(w http.ResponseWriter, e error) {
	if errors.Is(e, store.ErrNotFound) {
		fail(w, 404, "not_found", "Resource not found")
	} else {
		slog.Error("database operation failed")
		fail(w, 503, "unavailable", "Storage is temporarily unavailable")
	}
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if s.db.Ping(ctx) != nil || storage.Check(s.c.Root) != nil || s.probe(ctx) != nil {
		fail(w, 503, "not_ready", "Service is not ready")
		return
	}
	write(w, 200, map[string]string{"status": "ready"})
}
func (s *Server) capabilities(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"service_version": model.Version, "pipeline_version": model.PipelineVersion,
		"accepted_inputs": []string{"jpeg", "png"}, "formats": processing.Formats(), "profiles": s.c.Profiles, "limits": s.c.Limits, "public_variants": s.c.PublicVariants})
}
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 200 || strings.TrimSpace(key) != key {
		fail(w, 400, "invalid_idempotency_key", "Key must be at most 200 bytes without surrounding whitespace")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.c.Limits.UploadBytes+64*1024)
	mr, e := r.MultipartReader()
	if e != nil {
		fail(w, 400, "invalid_multipart", "Expected multipart form data")
		return
	}
	f, e := os.CreateTemp(filepath.Join(s.c.Root, "uploads"), "upload-")
	if e != nil {
		fail(w, 503, "unavailable", "Upload storage unavailable")
		return
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	defer f.Close()
	hash := sha256.New()
	hasFile, hasProfile := false, false
	profileName := "web"
	var size int64
	for parts := 0; ; parts++ {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			uploadError(w, err)
			return
		}
		if parts >= 2 {
			fail(w, 400, "invalid_multipart", "Only file and profile fields are allowed")
			return
		}
		switch part.FormName() {
		case "file":
			if hasFile {
				fail(w, 400, "invalid_multipart", "Exactly one file is required")
				return
			}
			hasFile = true
			size, err = io.Copy(io.MultiWriter(f, hash), io.LimitReader(part, s.c.Limits.UploadBytes+1))
			if size > s.c.Limits.UploadBytes {
				fail(w, 413, "upload_too_large", "Upload exceeds the configured byte limit")
				return
			}
		case "profile":
			if hasProfile {
				fail(w, 400, "invalid_multipart", "Only one profile is allowed")
				return
			}
			hasProfile = true
			var b []byte
			b, err = io.ReadAll(io.LimitReader(part, 65))
			if len(b) > 64 {
				fail(w, 400, "invalid_profile", "Profile name is too long")
				return
			}
			profileName = string(b)
		default:
			fail(w, 400, "invalid_multipart", "Unknown multipart field")
			return
		}
		if err != nil {
			uploadError(w, err)
			return
		}
	}
	if !hasFile || size == 0 {
		fail(w, 400, "missing_file", "A nonempty file is required")
		return
	}
	profile, ok := s.c.Profiles[profileName]
	if !ok {
		fail(w, 400, "invalid_profile", "Unknown configured profile")
		return
	}
	if e = f.Sync(); e != nil {
		fail(w, 503, "unavailable", "Upload storage unavailable")
		return
	}
	if e = f.Close(); e != nil {
		fail(w, 503, "unavailable", "Upload storage unavailable")
		return
	}
	select {
	case s.validationSlots <- struct{}{}:
		defer func() { <-s.validationSlots }()
	default:
		fail(w, 503, "busy", "Input validation capacity is busy; retry later")
		return
	}
	input, e := s.validate(r.Context(), tmp)
	if e != nil {
		fail(w, 422, "invalid_image", "Image is invalid, unsupported, or exceeds processing limits")
		return
	}
	planned := processing.Planned(input, profile)
	if planned > s.c.Limits.OutputCount {
		fail(w, 422, "output_limit", "Profile exceeds the output count limit for this image")
		return
	}
	imageID, jobID := storage.ID(), storage.ID()
	original := filepath.Join(s.c.Root, "originals", imageID)
	if e = os.Rename(tmp, original); e != nil {
		fail(w, 503, "unavailable", "Upload storage unavailable")
		return
	}
	// Persist the rename before committing a database reference to the original.
	if e = syncDir(filepath.Dir(original)); e != nil {
		_ = os.Remove(original)
		fail(w, 503, "unavailable", "Upload storage unavailable")
		return
	}
	actualImage, actualJob, existing, e := s.db.Accept(r.Context(), imageID, jobID, hex.EncodeToString(hash.Sum(nil)), key, input, profile, planned)
	// A connection error can leave commit outcome uncertain. Retain the file
	// for reconciliation rather than deleting a possibly committed original.
	if existing || errors.Is(e, store.ErrConflict) {
		_ = os.Remove(original)
	}
	if e != nil {
		if errors.Is(e, store.ErrConflict) {
			fail(w, 409, "idempotency_conflict", "Key already belongs to a different content or profile")
		} else {
			lookupError(w, e)
		}
		return
	}
	statusURL := "/v1/jobs/" + actualJob
	w.Header().Set("Location", statusURL)
	write(w, 202, map[string]string{"image_id": actualImage, "job_id": actualJob, "status_url": statusURL})
}
func uploadError(w http.ResponseWriter, e error) {
	var max *http.MaxBytesError
	if errors.As(e, &max) {
		fail(w, 413, "upload_too_large", "Request exceeds the configured byte limit")
	} else {
		fail(w, 400, "invalid_multipart", "Malformed or truncated multipart body")
	}
}
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func (s *Server) job(w http.ResponseWriter, r *http.Request) {
	j, e := s.db.Job(r.Context(), r.PathValue("id"))
	if e != nil {
		lookupError(w, e)
		return
	}
	write(w, 200, j)
}
func (s *Server) image(w http.ResponseWriter, r *http.Request) {
	i, e := s.db.Image(r.Context(), r.PathValue("id"))
	if e != nil {
		lookupError(w, e)
		return
	}
	write(w, 200, i)
}
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := 50, 0
	var e error
	if q.Has("limit") {
		limit, e = strconv.Atoi(q.Get("limit"))
		if e != nil || limit < 1 || limit > 100 {
			fail(w, 400, "invalid_pagination", "Limit must be between 1 and 100")
			return
		}
	}
	if q.Has("offset") {
		offset, e = strconv.Atoi(q.Get("offset"))
		if e != nil || offset < 0 || offset > 1_000_000 {
			fail(w, 400, "invalid_pagination", "Offset must be between 0 and 1000000")
			return
		}
	}
	status := q.Get("status")
	if status != "" && status != "queued" && status != "processing" && status != "succeeded" && status != "failed" {
		fail(w, 400, "invalid_status", "Unknown status")
		return
	}
	items, e := s.db.List(r.Context(), status, limit, offset)
	if e != nil {
		lookupError(w, e)
		return
	}
	if items == nil {
		items = []model.Image{}
	}
	write(w, 200, map[string]any{"images": items, "limit": limit, "offset": offset})
}
func (s *Server) original(w http.ResponseWriter, r *http.Request) {
	i, e := s.db.Image(r.Context(), r.PathValue("id"))
	if e != nil {
		lookupError(w, e)
		return
	}
	s.send(w, r, "originals/"+i.ID, i.Original.Format, false)
}
func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	o, e := s.db.Asset(r.Context(), r.PathValue("id"))
	if e != nil {
		lookupError(w, e)
		return
	}
	s.send(w, r, o.Path, o.Format, true)
}
func (s *Server) send(w http.ResponseWriter, r *http.Request, path, format string, variant bool) {
	path, e := storage.Path(s.c.Root, path)
	if e != nil {
		fail(w, 404, "not_found", "Asset not found")
		return
	}
	f, e := os.Open(path)
	if e != nil {
		fail(w, 404, "not_found", "Asset not found")
		return
	}
	defer f.Close()
	stat, e := f.Stat()
	if e != nil || !stat.Mode().IsRegular() {
		fail(w, 404, "not_found", "Asset not found")
		return
	}
	mime := map[string]string{"jpeg": "image/jpeg", "png": "image/png", "webp": "image/webp", "avif": "image/avif"}[format]
	if mime == "" {
		fail(w, 500, "invalid_asset", "Asset format unavailable")
		return
	}
	w.Header().Set("Content-Type", mime)
	if variant {
		access := "private"
		if s.c.PublicVariants {
			access = "public"
		}
		w.Header().Set("Cache-Control", access+", max-age=86400, immutable")
	}
	http.ServeContent(&downloadWriter{ResponseWriter: w}, r, "", stat.ModTime(), f)
}

type downloadWriter struct {
	http.ResponseWriter
	failed bool
}

func (w *downloadWriter) WriteHeader(status int) {
	if status >= 400 {
		w.failed = true
		w.Header().Del("Content-Length")
		w.Header().Set("Cache-Control", "no-store")
		fail(w.ResponseWriter, status, "download_failed", "The requested download or byte range is unavailable")
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *downloadWriter) Write(b []byte) (int, error) {
	if w.failed {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}
func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Read paths first; publication and this delete serialize in PostgreSQL.
	image, e := s.db.Image(r.Context(), id)
	if e != nil {
		lookupError(w, e)
		return
	}
	found, e := s.db.Delete(r.Context(), id)
	if e != nil {
		lookupError(w, e)
		return
	}
	if !found {
		fail(w, 404, "not_found", "Image not found")
		return
	}
	if path, e := storage.Path(s.c.Root, "originals/"+image.ID); e == nil {
		_ = os.Remove(path)
	}
	for _, o := range image.Outputs {
		if p, e := storage.Path(s.c.Root, o.Path); e == nil {
			_ = os.Remove(p)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
