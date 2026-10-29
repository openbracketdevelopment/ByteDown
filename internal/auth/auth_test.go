package auth

import (
	"net/http/httptest"
	"testing"
)

func TestBearer(t *testing.T) {
	for _, tc := range []struct {
		scheme, token string
		valid         bool
	}{
		{"", "", false}, {"wrong", "", false}, {"Bearer", "wrong", false},
		{"bearer", "test-key", false}, {"Bearer", "test-key extra", false},
		{"Bearer", "test-key", true},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		header := tc.scheme + " " + tc.token
		r.Header.Set("Authorization", header)
		if got := Valid(r, "test-key"); got != tc.valid {
			t.Fatalf("unexpected authentication result for %q", header)
		}
	}
}
