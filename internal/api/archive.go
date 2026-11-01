package api

import (
	"archive/zip"
	"fmt"
	"github.com/openbracketdevelopment/ByteDown/internal/storage"
	"io"
	"net/http"
	"os"
	"strings"
)

// archive accepts image IDs only, never user-provided paths or filenames.
func (s *Server) archive(w http.ResponseWriter, r *http.Request) {
	ids := strings.Split(r.URL.Query().Get("images"), ",")
	if len(ids) > 10 || len(ids) == 0 {
		fail(w, 400, "archive_limit", "Choose 1–10 completed images")
		return
	}
	type entry struct {
		f    *os.File
		name string
		size int64
	}
	var entries []entry
	defer func() {
		for _, e := range entries {
			e.f.Close()
		}
	}()
	seen := map[string]bool{}
	var total int64
	for _, id := range ids {
		if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" || seen[id] {
			fail(w, 400, "invalid_image", "Invalid or duplicate image ID")
			return
		}
		seen[id] = true
		image, e := s.db.Image(r.Context(), id)
		if e != nil {
			lookupError(w, e)
			return
		}
		if image.Status != "succeeded" || len(image.Outputs) == 0 {
			fail(w, 409, "incomplete_image", "All selected images must have completed outputs")
			return
		}
		for _, o := range image.Outputs {
			path, e := storage.Path(s.c.Root, o.Path)
			if e != nil {
				fail(w, 404, "not_found", "Output unavailable")
				return
			}
			f, e := os.Open(path)
			if e != nil {
				fail(w, 404, "not_found", "Output unavailable")
				return
			}
			stat, e := f.Stat()
			if e != nil || !stat.Mode().IsRegular() {
				f.Close()
				fail(w, 404, "not_found", "Output unavailable")
				return
			}
			name := fmt.Sprintf("%s/image-%dw-%s.%s", id, o.Width, o.ID, o.Format)
			entries = append(entries, entry{f, name, stat.Size()})
			total += stat.Size()
			if len(entries) > 160 || total > 512<<20 {
				fail(w, 413, "archive_limit", "Archive exceeds 160 files or 512 MiB")
				return
			}
		}
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="bytedown-results.zip"`)
	if r.Method == http.MethodHead {
		return
	}
	z := zip.NewWriter(w)
	for _, e := range entries {
		if r.Context().Err() != nil {
			return
		}
		dest, err := z.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Store})
		if err != nil {
			return
		}
		if _, err = io.CopyN(dest, e.f, e.size); err != nil {
			return
		}
	}
	_ = z.Close()
}
