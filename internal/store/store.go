package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openbracketdevelopment/ByteDown/internal/model"
	"github.com/openbracketdevelopment/ByteDown/migrations"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("idempotency key conflicts with an existing request")
	ErrLeaseLost = errors.New("worker lease lost")
)

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	s := &Store{pool: pool}
	if err = s.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(684731955)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (name text PRIMARY KEY)"); err != nil {
		return err
	}
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var applied bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)", entry.Name()).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		sql, readErr := migrations.Files.ReadFile(entry.Name())
		if readErr != nil {
			return readErr
		}
		if _, err = tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migration %s: %w", entry.Name(), err)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO schema_migrations(name) VALUES($1)", entry.Name()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) Accept(ctx context.Context, imageID, jobID, hash, key string, input model.Input, profile model.Profile, planned int) (string, string, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", "", false, err
	}
	defer tx.Rollback(ctx)
	profileJSON, err := json.Marshal(profile)
	if err != nil {
		return "", "", false, err
	}
	if key != "" {
		if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", key); err != nil {
			return "", "", false, err
		}
		var oldHash, oldImage, oldJob string
		var sameProfile bool
		err = tx.QueryRow(ctx, `SELECT k.hash,k.image_id,j.id,j.profile=$2::jsonb FROM idempotency k JOIN jobs j ON j.image_id=k.image_id WHERE k.key=$1`, key, profileJSON).Scan(&oldHash, &oldImage, &oldJob, &sameProfile)
		if err == nil {
			if oldHash != hash || !sameProfile {
				return "", "", false, ErrConflict
			}
			return oldImage, oldJob, true, tx.Commit(ctx)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", "", false, err
		}
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return "", "", false, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO images(id,input) VALUES($1,$2)", imageID, inputJSON); err != nil {
		return "", "", false, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO jobs(id,image_id,profile,pipeline,planned) VALUES($1,$2,$3,$4,$5)", jobID, imageID, profileJSON, model.PipelineVersion, planned); err != nil {
		return "", "", false, err
	}
	if key != "" {
		if _, err = tx.Exec(ctx, "INSERT INTO idempotency(key,hash,image_id) VALUES($1,$2,$3)", key, hash, imageID); err != nil {
			return "", "", false, err
		}
	}
	return imageID, jobID, false, tx.Commit(ctx)
}

const jobColumns = `id,image_id,status,generated,planned,error_code,error_message,duration_ms,profile,pipeline,COALESCE(token,''),attempt`

func scanJob(row pgx.Row) (model.Job, error) {
	var j model.Job
	var profile []byte
	err := row.Scan(&j.ID, &j.ImageID, &j.Status, &j.Generated, &j.Planned, &j.ErrorCode, &j.ErrorMessage, &j.DurationMS, &profile, &j.Pipeline, &j.Token, &j.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, ErrNotFound
	}
	if err != nil {
		return j, err
	}
	err = json.Unmarshal(profile, &j.Profile)
	return j, err
}

func (s *Store) Job(ctx context.Context, id string) (model.Job, error) {
	return scanJob(s.pool.QueryRow(ctx, "SELECT "+jobColumns+" FROM jobs WHERE id=$1", id))
}

func (s *Store) Image(ctx context.Context, id string) (model.Image, error) {
	// One snapshot ensures deletion/publication cannot produce mixed image metadata.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return model.Image{}, err
	}
	defer tx.Rollback(ctx)
	var image model.Image
	var input []byte
	err = tx.QueryRow(ctx, `SELECT i.id,j.id,j.status,i.input,i.created_at FROM images i JOIN jobs j ON j.image_id=i.id WHERE i.id=$1`, id).Scan(&image.ID, &image.JobID, &image.Status, &input, &image.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return image, ErrNotFound
	}
	if err != nil {
		return image, err
	}
	if err = json.Unmarshal(input, &image.Original); err != nil {
		return image, err
	}
	image.OriginalURL = "/v1/images/" + id + "/original"
	image.Outputs = []model.Output{}
	rows, err := tx.Query(ctx, "SELECT metadata,path FROM outputs WHERE image_id=$1 ORDER BY id", id)
	if err != nil {
		return image, err
	}
	for rows.Next() {
		var raw []byte
		var out model.Output
		if err = rows.Scan(&raw, &out.Path); err != nil {
			rows.Close()
			return image, err
		}
		if err = json.Unmarshal(raw, &out); err != nil {
			rows.Close()
			return image, err
		}
		image.Outputs = append(image.Outputs, out)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return image, err
	}
	return image, tx.Commit(ctx)
}

func (s *Store) List(ctx context.Context, status string, limit, offset int) ([]model.Image, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx, `SELECT i.id FROM images i JOIN jobs j ON j.image_id=i.id WHERE ($1='' OR j.status=$1) ORDER BY i.created_at DESC,i.id LIMIT $2 OFFSET $3`, status, limit, offset)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	images := make([]model.Image, 0, len(ids))
	for _, id := range ids {
		image, e := s.Image(ctx, id)
		if errors.Is(e, ErrNotFound) {
			continue
		}
		if e != nil {
			return nil, e
		}
		images = append(images, image)
	}
	return images, nil
}

func (s *Store) Asset(ctx context.Context, id string) (model.Output, error) {
	var out model.Output
	var raw []byte
	err := s.pool.QueryRow(ctx, "SELECT metadata,path FROM outputs WHERE id=$1", id).Scan(&raw, &out.Path)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

func (s *Store) Delete(ctx context.Context, id string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	// Publish locks the job first too, preventing a cascade-delete deadlock.
	var job string
	err = tx.QueryRow(ctx, "SELECT id FROM jobs WHERE image_id=$1 FOR UPDATE", id).Scan(&job)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, "DELETE FROM images WHERE id=$1", id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, tx.Commit(ctx)
}

// Claim atomically leases queued jobs or recovers abandoned attempts.
func (s *Store) Claim(ctx context.Context, lease time.Duration) (model.Job, error) {
	var tokenBytes [24]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return model.Job{}, err
	}
	token := hex.EncodeToString(tokenBytes[:])
	return scanJob(s.pool.QueryRow(ctx, `WITH exhausted AS (
		UPDATE jobs SET status='failed',error_code='worker_lost',error_message='Worker recovery limit reached',token=NULL,lease_until=NULL
		WHERE id IN (SELECT id FROM jobs WHERE status='processing' AND lease_until<clock_timestamp() AND attempt>=3 FOR UPDATE SKIP LOCKED)
	) UPDATE jobs SET status='processing',token=$1,lease_until=clock_timestamp()+$2*interval '1 second',attempt=attempt+1
		WHERE id=(SELECT id FROM jobs WHERE attempt<3 AND ((status='queued' AND available_at<=clock_timestamp()) OR (status='processing' AND lease_until<clock_timestamp()))
		ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING `+jobColumns, token, lease.Seconds()))
}

func (s *Store) Heartbeat(ctx context.Context, id, token string, lease time.Duration) error {
	tag, err := s.pool.Exec(ctx, `UPDATE jobs SET lease_until=clock_timestamp()+$3*interval '1 second' WHERE id=$1 AND token=$2 AND status='processing' AND lease_until>clock_timestamp()`, id, token, lease.Seconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) Fail(ctx context.Context, id, token, code, message string, duration time.Duration, retry bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE jobs SET status=CASE WHEN $6 AND attempt<3 THEN 'queued' ELSE 'failed' END,error_code=$3,error_message=$4,duration_ms=$5,token=NULL,lease_until=NULL,available_at=clock_timestamp()+interval '1 second' WHERE id=$1 AND token=$2 AND status='processing' AND lease_until>clock_timestamp()`, id, token, code, message, duration.Milliseconds(), retry)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) Publish(ctx context.Context, job model.Job, outputs []model.Output, duration time.Duration) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var imageID string
	err = tx.QueryRow(ctx, `SELECT image_id FROM jobs WHERE id=$1 AND token=$2 AND status='processing' AND lease_until>clock_timestamp() FOR UPDATE`, job.ID, job.Token).Scan(&imageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	for _, out := range outputs {
		raw, e := json.Marshal(out)
		if e != nil {
			return e
		}
		if _, err = tx.Exec(ctx, "INSERT INTO outputs(id,image_id,path,metadata) VALUES($1,$2,$3,$4)", out.ID, imageID, out.Path, raw); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE jobs SET status='succeeded',generated=$3,duration_ms=$4,error_code='',error_message='',lease_until=NULL WHERE id=$1 AND token=$2 AND lease_until>clock_timestamp()`, job.ID, job.Token, len(outputs), duration.Milliseconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return tx.Commit(ctx)
}

// Referenced checks cleanup candidates against live database ownership.
func (s *Store) Referenced(ctx context.Context, kind, id string) (bool, error) {
	var live bool
	var err error
	switch kind {
	case "originals":
		err = s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM images WHERE id=$1)", id).Scan(&live)
	case "staging":
		err = s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM jobs WHERE token=$1 AND status='processing')", id).Scan(&live)
	case "outputs":
		err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM outputs WHERE split_part(path,'/',2)=$1) OR EXISTS(SELECT 1 FROM jobs WHERE token=$1 AND status='processing')`, id).Scan(&live)
	default:
		return false, fmt.Errorf("unknown storage kind %q", kind)
	}
	return live, err
}
