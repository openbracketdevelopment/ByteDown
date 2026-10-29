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

func TestNativeConcurrency(t *testing.T) {
	for _, value := range []string{"1", "2", "16", "0", "17", "", "invalid"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("VIPS_CONCURRENCY", value)
			err := ValidateNativeConcurrency()
			valid := value == "1" || value == "2" || value == "16"
			if (err == nil) != valid {
				t.Fatalf("unexpected validation for %q: %v", value, err)
			}
		})
	}
}

func TestLowerDimensionLimitKeepsDefaultProfile(t *testing.T) {
	environment(t)
	t.Setenv("MAX_DIMENSION", "1000")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Limits.Dimension != 1000 || len(c.Profiles["web"].Widths) != 4 {
		t.Fatal("dimension limit unexpectedly changed the configured profile")
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
