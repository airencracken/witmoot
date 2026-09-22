package forum

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"regexp"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,24}$`)

func ValidateCredentials(username, password string) string {
	if !usernamePattern.MatchString(username) {
		return "Choose a username with 3–24 letters, numbers, underscores, or dashes."
	}
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 12 || len(password) > 72 {
		return "Use a password with at least 12 characters and at most 72 bytes."
	}
	return ""
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

func randomToken() string {
	b := make([]byte, 32)
	// crypto/rand.Read terminates the process if the system RNG fails.
	rand.Read(b)
	return hex.EncodeToString(b)
}

func tokenHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

type rateEntry struct {
	Count int
	Until time.Time
}

type limiter struct {
	mu      sync.Mutex
	entries map[string]rateEntry
}

func (l *limiter) allow(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, v := range l.entries {
		if !v.Until.After(now) {
			delete(l.entries, k)
		}
	}
	e, ok := l.entries[host]
	if !ok {
		if len(l.entries) >= 10000 {
			return false
		}
		e.Until = now.Add(15 * time.Minute)
	}
	if e.Count >= 20 {
		return false
	}
	e.Count++
	l.entries[host] = e
	return true
}
