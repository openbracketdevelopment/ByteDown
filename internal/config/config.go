package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/openbracketdevelopment/ByteDown/internal/model"
)

type Config struct {
	DatabaseURL    string
	APIKey         string
	Root           string
	Listen         string
	PublicVariants bool
	Origins        []string
	Concurrency    int
	Limits         model.Limits
	Profiles       map[string]model.Profile
}

func ValidateNativeConcurrency() error {
	value, present := os.LookupEnv("VIPS_CONCURRENCY")
	if !present {
		return nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 16 {
		return errors.New("VIPS_CONCURRENCY must be an integer from 1 to 16")
	}
	return nil
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL: os.Getenv("DATABASE_URL"), APIKey: os.Getenv("API_KEY"),
		Root: env("STORAGE_DIR", "/data"), Listen: env("LISTEN_ADDR", ":8080"),
		Profiles: map[string]model.Profile{"web": {
			Widths: []int{480, 960, 1440, 1920}, Formats: []string{"webp", "avif"},
			IncludeSource: true, JPEGQuality: 82, WebPQuality: 80, WebPEffort: 4, AVIFQuality: 50, AVIFEffort: 4,
		}},
	}
	if c.DatabaseURL == "" || len(c.APIKey) < 32 {
		return c, errors.New("DATABASE_URL and an API_KEY of at least 32 bytes are required")
	}
	publicVariants := env("PUBLIC_VARIANTS", "false")
	if publicVariants != "true" && publicVariants != "false" {
		return c, errors.New("PUBLIC_VARIANTS must be true or false")
	}
	c.PublicVariants = publicVariants == "true"
	var err error
	for _, v := range []struct {
		name     string
		fallback int64
		dst      *int64
	}{
		{"MAX_UPLOAD_BYTES", 20 << 20, &c.Limits.UploadBytes},
		{"MAX_PIXELS", 40_000_000, &c.Limits.Pixels},
		{"MAX_OUTPUT_BYTES", 100 << 20, &c.Limits.OutputBytes},
	} {
		*v.dst, err = positive(v.name, v.fallback)
		if err != nil {
			return c, err
		}
	}
	for _, v := range []struct {
		name     string
		fallback int64
		dst      *int
	}{
		{"MAX_DIMENSION", 20_000, &c.Limits.Dimension},
		{"MAX_OUTPUT_COUNT", 16, &c.Limits.OutputCount},
		{"WORKER_CONCURRENCY", 1, &c.Concurrency},
	} {
		n, e := positive(v.name, v.fallback)
		if e != nil {
			return c, e
		}
		if n > int64(math.MaxInt) {
			return c, fmt.Errorf("%s exceeds the platform integer limit", v.name)
		}
		*v.dst = int(n)
	}
	c.Limits.Duration, err = time.ParseDuration(env("PROCESS_TIMEOUT", "120s"))
	if err != nil || c.Limits.Duration <= 0 || c.Limits.Duration > 24*time.Hour {
		return c, errors.New("PROCESS_TIMEOUT must be positive and no more than 24h")
	}
	if c.Concurrency > 64 {
		return c, errors.New("WORKER_CONCURRENCY must not exceed 64")
	}
	if raw := os.Getenv("PROFILES_JSON"); raw != "" {
		c.Profiles = nil
		if err = json.Unmarshal([]byte(raw), &c.Profiles); err != nil {
			return c, errors.New("invalid PROFILES_JSON")
		}
	}
	if len(c.Profiles) == 0 {
		return c, errors.New("at least one profile is required")
	}
	for name, p := range c.Profiles {
		if name == "" || len(name) > 64 {
			return c, errors.New("invalid profile name")
		}
		if len(p.Widths) > 64 || len(p.Formats) == 0 || len(p.Formats) > 2 {
			return c, fmt.Errorf("profile %q requires one or two variant formats and at most 64 widths", name)
		}
		seen := map[string]bool{}
		for _, f := range p.Formats {
			if (f != "webp" && f != "avif") || seen[f] {
				return c, fmt.Errorf("invalid formats in profile %q", name)
			}
			seen[f] = true
		}
		for _, w := range p.Widths {
			if w <= 0 {
				return c, fmt.Errorf("invalid widths in profile %q", name)
			}
		}
		if !p.IncludeSource && len(p.Widths) == 0 {
			return c, errors.New("profile has no variant widths")
		}
		if p.JPEGQuality < 1 || p.JPEGQuality > 100 || p.WebPQuality < 1 || p.WebPQuality > 100 || p.AVIFQuality < 1 || p.AVIFQuality > 100 || p.WebPEffort < 0 || p.WebPEffort > 6 || p.AVIFEffort < 0 || p.AVIFEffort > 9 {
			return c, fmt.Errorf("invalid codec options in profile %q", name)
		}
	}
	for _, origin := range strings.Split(os.Getenv("CORS_ORIGINS"), ",") {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		u, e := url.Parse(origin)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return c, errors.New("CORS_ORIGINS must contain explicit http(s) origins without paths")
		}
		c.Origins = append(c.Origins, origin)
	}
	return c, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func positive(key string, fallback int64) (int64, error) {
	v, e := strconv.ParseInt(env(key, strconv.FormatInt(fallback, 10)), 10, 64)
	if e != nil || v <= 0 || v > 1<<50 {
		return 0, fmt.Errorf("%s must be a positive bounded integer", key)
	}
	return v, nil
}
