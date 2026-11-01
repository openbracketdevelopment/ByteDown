// Package browser adds optional operator sessions without changing bearer clients.
package browser

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openbracketdevelopment/ByteDown/docs"
	"github.com/openbracketdevelopment/ByteDown/internal/config"
	"github.com/openbracketdevelopment/ByteDown/web"
)

type session struct {
	csrf    string
	expires time.Time
}
type sessions struct {
	sync.Mutex
	values map[string]session
	next   time.Time
}

func token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Password hashes are pbkdf2-sha256$600000$<16-byte hex salt>$<32-byte hex hash>.
func CheckPassword(password, encoded string) bool {
	p := strings.Split(encoded, "$")
	if len(p) != 4 || p[0] != "pbkdf2-sha256" {
		return false
	}
	n, e := strconv.Atoi(p[1])
	if e != nil || n < 600000 || n > 2000000 {
		return false
	}
	salt, e := hex.DecodeString(p[2])
	if e != nil || len(salt) != 16 {
		return false
	}
	want, e := hex.DecodeString(p[3])
	if e != nil || len(want) != 32 {
		return false
	}
	got, e := pbkdf2.Key(sha256.New, password, salt, n, 32)
	return e == nil && subtle.ConstantTimeCompare(got, want) == 1
}
func Wrap(c config.Config, api http.Handler) http.Handler {
	if !c.UIEnabled {
		return api
	}
	state := &sessions{values: map[string]session{}}
	assets, _ := fs.Sub(web.Assets, "dist")
	static := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		// Reverse proxies must preserve Host and terminate HTTPS; cookie security uses
		// the browser Origin for login and the request's HTTPS forwarded scheme.
		secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
		originOK := func() bool {
			scheme := "http"
			if secure {
				scheme = "https"
			}
			return r.Header.Get("Origin") == scheme+"://"+r.Host
		}
		state.Lock()
		now := time.Now()
		for k, v := range state.values {
			if now.After(v.expires) {
				delete(state.values, k)
			}
		}
		cookie, _ := r.Cookie("bytedown_session")
		var current session
		var sid string
		if cookie != nil {
			sid = cookie.Value
			current = state.values[sid]
		}
		state.Unlock()
		authenticated := !current.expires.IsZero()
		respond := func(code int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(v)
		}
		if r.URL.Path == "/ui/login" && r.Method == "POST" {
			if !originOK() {
				http.Error(w, "Origin denied", 403)
				return
			}
			if c.OperatorHash == "" {
				http.Error(w, "Operator login is not configured", 503)
				return
			}
			state.Lock()
			limited := now.Before(state.next)
			if !limited {
				state.next = now.Add(time.Second)
			}
			state.Unlock()
			if limited {
				http.Error(w, "Please wait before trying again", 429)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			var input struct {
				Password string `json:"password"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || !CheckPassword(input.Password, c.OperatorHash) {
				http.Error(w, "Invalid password", 401)
				return
			}
			sid = token()
			current = session{token(), now.Add(8 * time.Hour)}
			state.Lock()
			if len(state.values) >= 128 {
				state.Unlock()
				http.Error(w, "Session limit reached", 429)
				return
			}
			state.values[sid] = current
			state.Unlock()
			http.SetCookie(w, &http.Cookie{Name: "bytedown_session", Value: sid, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: 28800})
			respond(200, map[string]string{"csrf": current.csrf})
			return
		}
		if r.URL.Path == "/ui/session" && r.Method == "GET" {
			if !authenticated {
				http.Error(w, "Sign in required", 401)
				return
			}
			respond(200, map[string]string{"csrf": current.csrf})
			return
		}
		if authenticated && (strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/media/") || r.URL.Path == "/ui/logout") && r.Header.Get("Authorization") == "" {
			if r.Method != "GET" && r.Method != "HEAD" && (!originOK() || r.Header.Get("X-CSRF-Token") != current.csrf) {
				http.Error(w, "CSRF check failed", 403)
				return
			}
			if r.URL.Path == "/ui/logout" {
				if r.Method != "POST" {
					http.Error(w, "Method not allowed", 405)
					return
				}
				state.Lock()
				delete(state.values, sid)
				state.Unlock()
				http.SetCookie(w, &http.Cookie{Name: "bytedown_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
				w.WriteHeader(204)
				return
			}
			clone := r.Clone(r.Context())
			clone.Header.Set("Authorization", "Bearer "+c.APIKey)
			api.ServeHTTP(w, clone)
			return
		}
		if r.Method == "GET" && (r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/assets/") || r.URL.Path == "/logo.svg") {
			static.ServeHTTP(w, r)
			return
		}
		documentAssets := map[string]struct{ name, mime string }{
			"/api-docs":                  {"reference.html", "text/html; charset=utf-8"},
			"/openapi.yaml":              {"openapi.yaml", "text/plain; charset=utf-8"},
			"/docs-assets/reference.css": {"reference.css", "text/css; charset=utf-8"},
			"/docs-assets/reference.js":  {"reference.js", "text/javascript; charset=utf-8"},
		}
		if asset, ok := documentAssets[r.URL.Path]; ok && r.Method == "GET" {
			content, err := docs.Assets.ReadFile(asset.name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", asset.mime)
			_, _ = w.Write(content)
			return
		}
		api.ServeHTTP(w, r)
	})
}
