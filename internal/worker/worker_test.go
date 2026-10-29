package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openbracketdevelopment/ByteDown/internal/model"
	"github.com/openbracketdevelopment/ByteDown/internal/store"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "process" {
		var task model.Task
		if json.NewDecoder(os.Stdin).Decode(&task) != nil {
			os.Exit(1)
		}
		if os.Getenv("BYTEDOWN_TEST_PROCESS") == "hang" {
			time.Sleep(time.Minute)
			os.Exit(1)
		}
		name := "jpeg-480.jpg"
		if os.Getenv("BYTEDOWN_TEST_PROCESS") == "jpeg-extension" {
			name = "jpeg-480.jpeg"
		}
		if os.WriteFile(filepath.Join(task.OutputDir, name), []byte("output"), 0600) != nil {
			os.Exit(1)
		}
		_ = json.NewEncoder(os.Stdout).Encode([]model.Output{{Format: "jpeg", Width: 480, Height: 320, Bytes: 6}})
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func workerStore(t *testing.T) *store.Store {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("worker_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	dsn := databaseURL + " search_path=" + schema
	if strings.HasPrefix(databaseURL, "postgres://") || strings.HasPrefix(databaseURL, "postgresql://") {
		parsed, parseErr := url.Parse(databaseURL)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		dsn = parsed.String()
	}
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	})
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return db
}

func workerRoot(t *testing.T) string {
	t.Helper()
	// Keep all test artifacts inside the checkout, never in the system temp directory.
	root, err := os.MkdirTemp(".", "worker-test-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	for _, kind := range []string{"originals", "staging", "outputs", "uploads"} {
		if err = os.Mkdir(filepath.Join(root, kind), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func workerJob(t *testing.T, db *store.Store) model.Job {
	t.Helper()
	ctx := context.Background()
	_, _, _, err := db.Accept(ctx, "image", "job", "hash", "", model.Input{Format: "jpeg", Width: 480, Height: 320, Bytes: 100}, model.Profile{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	job, err := db.Claim(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestProcessorPublicationAndDeadline(t *testing.T) {
	for _, mode := range []string{"success", "jpeg-extension", "hang"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("BYTEDOWN_TEST_PROCESS", mode)
			db := workerStore(t)
			root := workerRoot(t)
			job := workerJob(t, db)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			duration := 5 * time.Second
			if mode == "hang" {
				duration = 100 * time.Millisecond
			}
			start := time.Now()
			process(context.Background(), db, root, executable, model.Limits{Duration: duration, OutputCount: 2, OutputBytes: 1024}, job)
			got, err := db.Job(context.Background(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "hang" {
				if got.Status != "failed" || got.ErrorCode != "processing_timeout" || time.Since(start) > 3*time.Second {
					t.Fatalf("deadline did not kill processor: %+v", got)
				}
			} else {
				image, err := db.Image(context.Background(), job.ImageID)
				if err != nil || got.Status != "succeeded" || len(image.Outputs) != 1 {
					t.Fatalf("not published: %+v %v", image, err)
				}
				if _, err = os.Stat(filepath.Join(root, image.Outputs[0].Path)); err != nil {
					t.Fatal(err)
				}
				directory := filepath.Dir(filepath.Join(root, image.Outputs[0].Path))
				old := time.Now().Add(-time.Hour)
				if err = os.Chtimes(directory, old, old); err != nil {
					t.Fatal(err)
				}
				if err = Reconcile(context.Background(), db, root, time.Minute); err != nil {
					t.Fatal(err)
				}
				if _, err = os.Stat(filepath.Join(root, image.Outputs[0].Path)); err != nil {
					t.Fatal("reconciler removed published output:", err)
				}
			}
		})
	}
}

func TestReconcileRespectsOwnershipAndGrace(t *testing.T) {
	db := workerStore(t)
	root := workerRoot(t)
	job := workerJob(t, db)
	old := time.Now().Add(-time.Hour)
	for _, name := range []string{"staging/" + job.Token, "outputs/" + job.Token, "staging/orphan", "outputs/orphan", "originals/image", "originals/orphan", "originals/recent", "uploads/orphan", "uploads/recent"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
		if name != "originals/recent" && name != "uploads/recent" {
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := Reconcile(context.Background(), db, root, time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"staging/" + job.Token, "outputs/" + job.Token, "originals/image", "originals/recent", "uploads/recent"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("removed live/recent path %s: %v", name, err)
		}
	}
	for _, name := range []string{"staging/orphan", "outputs/orphan", "originals/orphan", "uploads/orphan"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("orphan survives %s: %v", name, err)
		}
	}
	if _, err := db.Delete(context.Background(), job.ImageID); err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(context.Background(), db, root, time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"staging/" + job.Token, "outputs/" + job.Token, "originals/image"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("deleted ownership retained %s: %v", name, err)
		}
	}
}

func TestOutputNames(t *testing.T) {
	name, err := outputName(model.Output{Format: "jpeg", Width: 480})
	if err != nil || name != "jpeg-480.jpeg" {
		t.Fatalf("bad filename %q %v", name, err)
	}

	for _, out := range []model.Output{{Format: "../jpeg", Width: 480}, {Format: "jpeg", Width: 0}} {
		if _, err = outputName(out); err == nil {
			t.Fatalf("accepted unsafe metadata: %+v", out)
		}
	}
}

func TestInfrastructureFailureRetries(t *testing.T) {
	db := workerStore(t)
	root := workerRoot(t)
	job := workerJob(t, db)
	process(context.Background(), db, root, filepath.Join(root, "nonexistent-executable"), model.Limits{Duration: time.Second, OutputCount: 1, OutputBytes: 1024}, job)
	got, err := db.Job(context.Background(), job.ID)
	if err != nil || got.Status != "queued" || got.ErrorCode != "processor_unavailable" {
		t.Fatalf("infrastructure error was not retryable: %+v %v", got, err)
	}
}

func TestUnavailablePipelineFailsWithoutExecuting(t *testing.T) {
	db := workerStore(t)
	root := workerRoot(t)
	job := workerJob(t, db)
	job.Pipeline = "unavailable-future-version"
	process(context.Background(), db, root, filepath.Join(root, "nonexistent-executable"), model.Limits{Duration: time.Second, OutputCount: 1, OutputBytes: 1024}, job)
	got, err := db.Job(context.Background(), job.ID)
	if err != nil || got.Status != "failed" || got.ErrorCode != "pipeline_unavailable" {
		t.Fatalf("unavailable pipeline executed or retried: %+v %v", got, err)
	}
}
