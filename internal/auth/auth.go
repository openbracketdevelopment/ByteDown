package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

func Valid(r *http.Request, key string) bool {
	header := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return false
	}
	// Random bearer keys are not passwords. Fixed-size digests make comparison
	// independent of token length; these hashes are never stored.
	got, want := sha256.Sum256([]byte(token)), sha256.Sum256([]byte(key))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}
