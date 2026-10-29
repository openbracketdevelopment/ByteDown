package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/openbracketdevelopment/ByteDown/internal/api"
	"github.com/openbracketdevelopment/ByteDown/internal/config"
	"github.com/openbracketdevelopment/ByteDown/internal/model"
	"github.com/openbracketdevelopment/ByteDown/internal/processing"
	"github.com/openbracketdevelopment/ByteDown/internal/storage"
	"github.com/openbracketdevelopment/ByteDown/internal/store"
	"github.com/openbracketdevelopment/ByteDown/internal/worker"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if e := run(); e != nil {
		// Native/driver errors may contain infrastructure paths or credentials.
		// Keep details off stdout and HTTP; callers receive only this safe class.
		slog.Error("command failed", "reason", safeError(e))
		var pathError *os.PathError
		if len(os.Args) > 1 && os.Args[1] == "process" && errors.As(e, &pathError) {
			// EX_TEMPFAIL distinguishes retryable filesystem failures from
			// permanently unsupported input or encoding configurations.
			os.Exit(75)
		}
		os.Exit(1)
	}
}

func safeError(e error) string {
	var safe *commandError
	if errors.As(e, &safe) {
		return safe.Error()
	}
	return "operation failed; check configuration and service dependencies"
}

type commandError struct{ message string }

func (e *commandError) Error() string { return e.message }

func run() error {
	command := "api"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command == "healthcheck" {
		addr := os.Getenv("LISTEN_ADDR")
		if addr == "" || strings.HasPrefix(addr, ":") {
			addr = "127.0.0.1" + addr
			if addr == "127.0.0.1" {
				addr += ":8080"
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/health/ready", nil)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			return e
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return errors.New("not ready")
		}
		return nil
	}
	if command == "probe" {
		return processing.Probe()
	}
	if command == "process" || command == "validate" {
		var task model.Task
		if e := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20)).Decode(&task); e != nil {
			return e
		}
		if command == "validate" {
			in, e := processing.Validate(task.InputPath, task.Limits)
			if e != nil {
				return e
			}
			return json.NewEncoder(os.Stdout).Encode(in)
		}
		out, e := processing.Process(task)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	if command != "api" && command != "worker" && command != "migrate" {
		return &commandError{"usage: bytedown api|worker|migrate|probe|healthcheck"}
	}
	c, e := config.Load()
	if e != nil {
		return &commandError{e.Error()}
	}
	if e = storage.Init(c.Root); e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	db, e := store.Open(ctx, c.DatabaseURL)
	if e != nil {
		return e
	}
	defer db.Close()
	if command == "migrate" {
		return db.Migrate(ctx)
	}
	// The parent never initializes libvips. It only starts bounded children.
	executable, e := os.Executable()
	if e != nil {
		return e
	}
	probeCtx, cancel := context.WithTimeout(ctx, c.Limits.Duration)
	e = child(probeCtx, executable, "probe", nil, nil)
	cancel()
	if e != nil {
		return &commandError{"required JPEG, PNG, WebP or AVIF processing capability is unavailable"}
	}
	if command == "worker" {
		return worker.Run(ctx, db, c.Root, c.Limits, c.Concurrency)
	}
	validate := func(ctx context.Context, path string) (model.Input, error) {
		ctx, cancel := context.WithTimeout(ctx, c.Limits.Duration)
		defer cancel()
		var input model.Input
		e := child(ctx, executable, "validate", model.Task{InputPath: path, Limits: c.Limits}, &input)
		return input, e
	}
	// Encoder capabilities belong to this immutable executable/system image.
	// Startup performs the real smoke test; readiness reuses that result.
	probe := func(ctx context.Context) error { return nil }
	server := &http.Server{Addr: c.Listen, Handler: api.New(c, db, validate, probe),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: c.Limits.Duration + 30*time.Second,
		WriteTimeout: c.Limits.Duration + 30*time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	done := make(chan error, 1)
	go func() { slog.Info("API listening", "address", c.Listen); done <- server.ListenAndServe() }()
	select {
	case e = <-done:
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return e
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func child(ctx context.Context, executable, command string, input, output any) error {
	cmd := exec.CommandContext(ctx, executable, command)
	if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return e
		}
		cmd.Stdin = strings.NewReader(string(b))
	}
	// Never forward codec diagnostics (which may contain metadata) to logs.
	b, e := cmd.Output()
	if e != nil {
		return fmt.Errorf("processing child failed: %w", ctxOrError(ctx, e))
	}
	if len(b) > 1<<20 {
		return errors.New("child response limit exceeded")
	}
	if output != nil {
		return json.Unmarshal(b, output)
	}
	return nil
}
func ctxOrError(ctx context.Context, e error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return e
}
