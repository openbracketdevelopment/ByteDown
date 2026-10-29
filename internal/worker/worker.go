package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openbracketdevelopment/ByteDown/internal/model"
	"github.com/openbracketdevelopment/ByteDown/internal/store"
	"github.com/openbracketdevelopment/ByteDown/internal/worker/filename"
)

const leaseDuration = 30 * time.Second

// Run supervises isolated processors; encoding never runs in the API process.
func Run(ctx context.Context, db *store.Store, root string, limits model.Limits, concurrency int) error {
	if concurrency < 1 {
		return errors.New("worker concurrency must be positive")
	}
	if limits.Duration <= 0 {
		return errors.New("processing duration must be positive")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root = absolute
	for _, kind := range []string{"originals", "staging", "outputs", "uploads"} {
		if err = os.MkdirAll(filepath.Join(root, kind), 0700); err != nil {
			return err
		}
		if err = syncPath(filepath.Join(root, kind)); err != nil {
			return err
		}
	}
	if err = syncPath(root); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	// Covers upload read timeout, isolated validation, and bounded acceptance.
	grace := 3*limits.Duration + 2*leaseDuration
	if err = Reconcile(ctx, db, root, grace); err != nil && ctx.Err() == nil {
		slog.Warn("storage reconciliation failed", "class", "storage")
	}
	var wg sync.WaitGroup
	wg.Add(concurrency + 1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e := Reconcile(ctx, db, root, grace); e != nil && ctx.Err() == nil {
					slog.Warn("storage reconciliation failed", "class", "storage")
				}
			}
		}
	}()
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				job, e := db.Claim(ctx, leaseDuration)
				if e != nil {
					if !errors.Is(e, store.ErrNotFound) && ctx.Err() == nil {
						slog.Warn("job claim failed", "class", "database")
					}
					if !pause(ctx, 500*time.Millisecond) {
						return
					}
					continue
				}
				process(ctx, db, root, executable, limits, job)
			}
		}()
	}
	wg.Wait()
	return nil
}

func pause(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (b *boundedBuffer) Bytes() []byte { return b.buffer.Bytes() }
func (b *boundedBuffer) Len() int      { return b.buffer.Len() }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if remaining < 0 {
		remaining = 0
	}
	if len(p) > remaining {
		p = p[:remaining]
		if !b.overflow {
			b.overflow = true
			if b.cancel != nil {
				b.cancel()
			}
		}
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func process(ctx context.Context, db *store.Store, root, executable string, limits model.Limits, job model.Job) {
	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, limits.Duration)
	defer cancel()
	fail := func(code, message string, retry bool) {
		if ctx.Err() != nil {
			return
		} // A shutting-down worker leaves its lease for recovery.
		failCtx, done := context.WithTimeout(ctx, 5*time.Second)
		defer done()
		if e := db.Fail(failCtx, job.ID, job.Token, code, message, time.Since(start), retry); e != nil && !errors.Is(e, store.ErrLeaseLost) {
			slog.Warn("record job failure", "job", job.ID, "class", "database")
		}
	}
	if job.Pipeline != model.PipelineVersion {
		fail("pipeline_unavailable", "Stored pipeline version is unavailable", false)
		return
	}
	if !component(job.ImageID) || !component(job.Token) {
		fail("storage_error", "Invalid stored identifier", false)
		return
	}
	stage := filepath.Join(root, "staging", job.Token)
	if err := os.Mkdir(stage, 0700); err != nil {
		fail("storage_error", "Cannot create processing directory", true)
		return
	}
	heartbeatDone := make(chan struct{})
	defer func() { cancel(); <-heartbeatDone }()
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(leaseDuration / 3)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				beatCtx, beatCancel := context.WithTimeout(runCtx, leaseDuration/3)
				e := db.Heartbeat(beatCtx, job.ID, job.Token, leaseDuration)
				if e == nil {
					count, countErr := countStaged(stage)
					if countErr == nil {
						e = db.Progress(beatCtx, job.ID, job.Token, count)
					}
				}
				beatCancel()
				if e != nil {
					cancel()
					return
				}
			}
		}
	}()
	task := model.Task{InputPath: filepath.Join(root, "originals", job.ImageID), OutputDir: stage, Profile: job.Profile, Limits: limits}
	payload, err := json.Marshal(task)
	if err != nil {
		fail("internal_error", "Cannot serialize processing request", false)
		return
	}
	cmd := exec.CommandContext(runCtx, executable, "process")
	cmd.Stdin = bytes.NewReader(payload)
	stdout := &boundedBuffer{limit: 2 << 20, cancel: cancel}
	stderr := &boundedBuffer{limit: 4096}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	if stdout.overflow {
		fail("processor_protocol", "Processor response limit exceeded", false)
		return
	}
	if err != nil {
		switch {
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			fail("processing_timeout", "Processing deadline exceeded", false)
		case runCtx.Err() != nil:
			return
		default:
			var exit *exec.ExitError
			retry := !errors.As(err, &exit) || exit.ExitCode() < 0 || exit.ExitCode() == 75
			code, message := "processing_failed", "Image processor failed"
			if retry {
				code, message = "processor_unavailable", "Image processor unavailable"
			}
			fail(code, message, retry)
		}
		return
	}
	var outputs []model.Output
	if err = json.Unmarshal(stdout.Bytes(), &outputs); err != nil {
		fail("processor_protocol", "Invalid processor response", false)
		return
	}
	if len(outputs) == 0 || len(outputs) > limits.OutputCount || len(outputs) != job.Planned {
		fail("processor_protocol", "Invalid output count", false)
		return
	}
	seen := make(map[string]bool, len(outputs))
	var total int64
	for i := range outputs {
		out := &outputs[i]
		name, e := outputName(*out)
		if e != nil || seen[name] {
			fail("processor_protocol", "Invalid output metadata", false)
			return
		}
		seen[name] = true
		info, e := os.Lstat(filepath.Join(stage, name))
		if errors.Is(e, fs.ErrNotExist) && out.Format == "jpeg" {
			// Both conventional JPEG extensions are deterministic, never processor-supplied paths.
			name = fmt.Sprintf("jpeg-%d.jpeg", out.Width)
			info, e = os.Lstat(filepath.Join(stage, name))
		}
		if e != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() != out.Bytes || out.Height < 1 {
			fail("processor_protocol", "Invalid output file", false)
			return
		}
		if info.Size() > limits.OutputBytes-total {
			fail("output_limit", "Output byte limit exceeded", false)
			return
		}
		total += info.Size()
		if e = syncPath(filepath.Join(stage, name)); e != nil {
			fail("storage_error", "Cannot persist output file", true)
			return
		}
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", job.ID, out.Format, out.Width)))
		out.ID = hex.EncodeToString(sum[:16])
		out.URL = "/media/" + out.ID
		out.Path = "outputs/" + job.Token + "/" + name
	}
	if runCtx.Err() != nil {
		fail("processing_timeout", "Processing deadline exceeded", false)
		return
	}
	if err = db.Heartbeat(runCtx, job.ID, job.Token, leaseDuration); err != nil {
		return
	}
	if err = syncPath(stage); err != nil {
		fail("storage_error", "Cannot persist output directory", true)
		return
	}
	// The entire attempt becomes visible on disk before its rows are committed.
	// Losing the lease or deleting the image leaves an unreferenced directory for reconciliation.
	if err = os.Rename(stage, filepath.Join(root, "outputs", job.Token)); err != nil {
		fail("storage_error", "Cannot finalize outputs", true)
		return
	}
	for _, directory := range []string{filepath.Join(root, "outputs"), filepath.Join(root, "staging"), root} {
		if err = syncPath(directory); err != nil {
			fail("storage_error", "Cannot persist finalized outputs", true)
			return
		}
	}
	if err = db.Publish(runCtx, job, outputs, time.Since(start)); err != nil && !errors.Is(err, store.ErrLeaseLost) {
		fail("storage_error", "Cannot publish outputs", true)
	}
}

func syncPath(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func component(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, `/\`)
}

func outputName(out model.Output) (string, error) {
	return filename.For(out.Format, out.Width)
}

func countStaged(directory string) (int, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		format, suffix, ok := strings.Cut(entry.Name(), "-")
		if !ok {
			continue
		}
		widthString, _, ok := strings.Cut(suffix, ".")
		if !ok {
			continue
		}
		width, err := strconv.Atoi(widthString)
		if err != nil {
			continue
		}
		name, err := filename.For(format, width)
		if err != nil || name != entry.Name() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return 0, err
		}
		if info.Mode().IsRegular() && info.Size() > 0 {
			count++
		}
	}
	return count, nil
}

// Reconcile only removes old, unreferenced attempts. Database errors fail closed.
// Callers must allow enough grace for uploads and an entire processing attempt.
func Reconcile(ctx context.Context, db *store.Store, root string, grace time.Duration) error {
	if grace < 2*leaseDuration {
		grace = 2 * leaseDuration
	}
	cutoff := time.Now().Add(-grace)
	for _, kind := range []string{"staging", "outputs", "originals", "uploads"} {
		directory := filepath.Join(root, kind)
		entries, err := os.ReadDir(directory)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err = ctx.Err(); err != nil {
				return err
			}
			info, e := entry.Info()
			if e != nil {
				if errors.Is(e, fs.ErrNotExist) {
					continue
				}
				return e
			}
			if !info.ModTime().Before(cutoff) {
				continue
			}
			live := false
			if kind != "uploads" {
				live, e = db.Referenced(ctx, kind, entry.Name())
				if e != nil {
					return e
				}
			}
			if live {
				continue
			}
			if e = os.RemoveAll(filepath.Join(directory, entry.Name())); e != nil {
				return e
			}
		}
	}
	return nil
}
