package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openbracketdevelopment/ByteDown/internal/model"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("store_test_%d", time.Now().UnixNano())
	if _, err = admin.pool.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	db := &Store{pool: pool}
	t.Cleanup(func() {
		db.Close()
		_, _ = admin.pool.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	})
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx); err != nil {
		t.Fatal("migration is not repeatable:", err)
	}
	return db
}

func accept(t *testing.T, db *Store, image, job, key string) {
	t.Helper()
	_, _, _, err := db.Accept(context.Background(), image, job, "hash", key, model.Input{Format: "jpeg", Width: 480, Height: 320, Bytes: 100}, model.Profile{Widths: []int{480}, Formats: []string{"jpeg"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentIdempotencyAndClaims(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	const count = 12
	type result struct {
		image, job string
		existing   bool
		err        error
	}
	results := make(chan result, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a, b, c, e := db.Accept(ctx, fmt.Sprintf("image-%d", i), fmt.Sprintf("job-%d", i), "hash", "same-key", model.Input{Format: "jpeg"}, model.Profile{}, 1)
			results <- result{a, b, c, e}
		}(i)
	}
	wg.Wait()
	close(results)
	var image, job string
	accepted := 0
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if image == "" {
			image, job = r.image, r.job
		}
		if image != r.image || job != r.job {
			t.Fatal("idempotency returned different identities")
		}
		if !r.existing {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d requests", accepted)
	}
	_, _, _, err := db.Accept(ctx, "conflict-image", "conflict-job", "different", "same-key", model.Input{}, model.Profile{}, 1)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	claims := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := db.Claim(ctx, time.Minute); claims <- e }()
	}
	wg.Wait()
	close(claims)
	claimed := 0
	for err := range claims {
		if err == nil {
			claimed++
		} else if !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	if claimed != 1 {
		t.Fatalf("claimed same job %d times", claimed)
	}
}

func TestRecoveryPublicationAndDeletionFencing(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	accept(t, db, "image", "job", "key")
	first, err := db.Claim(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.pool.Exec(ctx, "UPDATE jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id='job'"); err != nil {
		t.Fatal(err)
	}
	second, err := db.Claim(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second.Token == first.Token || second.Attempt != 2 {
		t.Fatal("recovery did not fence the old attempt")
	}
	if err = db.Heartbeat(ctx, first.ID, first.Token, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old heartbeat: %v", err)
	}
	out := model.Output{ID: "output", Path: "outputs/" + second.Token + "/jpeg-480.jpg", Format: "jpeg", Width: 480, Height: 320, Bytes: 60, URL: "/media/output"}
	if err = db.Publish(ctx, first, []model.Output{out}, time.Second); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old publish: %v", err)
	}
	if err = db.Publish(ctx, second, []model.Output{out}, time.Second); err != nil {
		t.Fatal(err)
	}
	asset, err := db.Asset(ctx, "output")
	if err != nil || asset.Path != out.Path {
		t.Fatalf("path not persisted: %+v %v", asset, err)
	}
	image, err := db.Image(ctx, "image")
	if err != nil || image.Status != "succeeded" || len(image.Outputs) != 1 || image.Outputs[0].Path != out.Path {
		t.Fatalf("bad published image: %+v %v", image, err)
	}
	deleted, err := db.Delete(ctx, "image")
	if err != nil || !deleted {
		t.Fatalf("delete: %v %v", deleted, err)
	}
	if _, err = db.Asset(ctx, "output"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("asset survived delete: %v", err)
	}
	if _, err = db.Job(ctx, "job"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("job survived delete: %v", err)
	}
	accept(t, db, "new-image", "new-job", "key") // Idempotency row cascaded too.
	third, err := db.Claim(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Delete(ctx, "new-image"); err != nil {
		t.Fatal(err)
	}
	if err = db.Publish(ctx, third, []model.Output{out}, time.Second); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("deleted worker published: %v", err)
	}
}

func TestExpiredLeaseAndBoundedRecovery(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	accept(t, db, "image", "job", "")
	for i := 1; i <= 3; i++ {
		job, err := db.Claim(ctx, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if job.Attempt != i {
			t.Fatalf("attempt %d, want %d", job.Attempt, i)
		}
		_, err = db.pool.Exec(ctx, "UPDATE jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id='job'")
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Publish(ctx, job, nil, time.Second); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("expired worker published: %v", err)
		}
	}
	if _, err := db.Claim(ctx, time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("exhausted job claimed: %v", err)
	}
	job, err := db.Job(ctx, "job")
	if err != nil || job.Status != "failed" || job.ErrorCode != "worker_lost" {
		t.Fatalf("bad terminal recovery: %+v %v", job, err)
	}
}

func TestTransientFailureRetry(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	accept(t, db, "image", "job", "")
	for i := 1; i <= 3; i++ {
		job, err := db.Claim(ctx, time.Minute)
		if err != nil {
			t.Fatal(err)
		}

		if err = db.Fail(ctx, job.ID, job.Token, "storage_error", "retry", time.Second, true); err != nil {
			t.Fatal(err)
		}
		got, err := db.Job(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if i < 3 {
			if got.Status != "queued" {
				t.Fatalf("not queued: %+v", got)
			}
			if _, err = db.pool.Exec(ctx, "UPDATE jobs SET available_at=clock_timestamp()"); err != nil {
				t.Fatal(err)
			}
		} else if got.Status != "failed" {
			t.Fatalf("not bounded: %+v", got)
		}
	}
}

func TestClaimSkipsLockedJob(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	accept(t, db, "image-one", "job-one", "")
	accept(t, db, "image-two", "job-two", "")
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT id FROM jobs WHERE id='job-one' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	claimCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	job, err := db.Claim(claimCtx, time.Minute)
	if err != nil || job.ID != "job-two" {
		t.Fatalf("claim did not skip row lock: %+v %v", job, err)
	}
}

func TestConcurrentDeleteAndPublication(t *testing.T) {
	db := testStore(t)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		imageID, jobID := fmt.Sprintf("image-%d", i), fmt.Sprintf("job-%d", i)
		accept(t, db, imageID, jobID, "")
		job, err := db.Claim(ctx, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		go func() {
			<-start
			_, e := db.Delete(ctx, imageID)
			results <- e
		}()
		go func() {
			<-start
			results <- db.Publish(ctx, job, []model.Output{{ID: fmt.Sprintf("out-%d", i), Path: fmt.Sprintf("outputs/%s/out.jpg", job.Token)}}, time.Second)
		}()
		close(start)
		for n := 0; n < 2; n++ {
			if e := <-results; e != nil && !errors.Is(e, ErrLeaseLost) {
				t.Fatal(e)
			}
		}
		if _, e := db.Image(ctx, imageID); !errors.Is(e, ErrNotFound) {
			t.Fatalf("deleted image survived: %v", e)
		}
		if _, e := db.Asset(ctx, fmt.Sprintf("out-%d", i)); !errors.Is(e, ErrNotFound) {
			t.Fatalf("deleted output survived: %v", e)
		}
	}
}
