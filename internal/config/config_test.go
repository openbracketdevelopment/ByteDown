package config

import (
	"strings"
	"testing"
)

func environment(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://localhost/bytedown")
	t.Setenv("API_KEY", strings.Repeat("x", 32))
	for _, k := range []string{"PROFILES_JSON", "CORS_ORIGINS", "PUBLIC_VARIANTS", "MAX_UPLOAD_BYTES", "MAX_PIXELS", "MAX_DIMENSION", "MAX_OUTPUT_COUNT", "MAX_OUTPUT_BYTES", "PROCESS_TIMEOUT", "WORKER_CONCURRENCY"} {
		t.Setenv(k, "")
	}
}
func TestDefaults(t *testing.T) {
	environment(t)
	c, e := Load()
	if e != nil {
		t.Fatal(e)
	}
	if c.PublicVariants || c.Concurrency != 1 || c.Limits.UploadBytes != 20<<20 || !c.Profiles["web"].IncludeSource {
		t.Fatal("unsafe or unexpected defaults")
	}
}
func TestRejectInvalid(t *testing.T) {
	for _, tc := range []struct{ k, v string }{
		{"API_KEY", "short"}, {"DATABASE_URL", ""}, {"MAX_UPLOAD_BYTES", "-1"},
		{"PROCESS_TIMEOUT", "0s"}, {"WORKER_CONCURRENCY", "65"},
		{"PUBLIC_VARIANTS", "yes"}, {"PUBLIC_VARIANTS", "null"}, {"CORS_ORIGINS", "*"}, {"CORS_ORIGINS", "https://example.com/path"},
		{"PROFILES_JSON", `{"web":{"widths":[480],"formats":["gif"]}}`},
		{"PROFILES_JSON", `{}`},
	} {
		t.Run(tc.k+tc.v, func(t *testing.T) {
			environment(t)
			t.Setenv(tc.k, tc.v)
			if _, e := Load(); e == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}
