package forum

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
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
	return ValidatePassword(password)
}

// ValidatePassword checks a password on its own, so a reset can be rejected
// before a one-time link is spent.
func ValidatePassword(password string) string {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 12 || len(password) > 72 {
		return "Use a password with at least 12 characters and at most 72 bytes."
	}
	return ""
}

// validEmail is deliberately permissive: an owner can hand over a reset link
// whatever the address looks like. It only rejects values that could not be a
// single address or that would let a newline into a mail header.
func validEmail(email string) bool {
	if email == "" {
		return true
	}
	if len(email) > 254 || strings.ContainsAny(email, "\r\n\t ") {
		return false
	}
	at := strings.IndexByte(email, '@')
	if at <= 0 || at == len(email)-1 || strings.Count(email, "@") != 1 {
		return false
	}
	if strings.HasPrefix(email, ".") || strings.HasSuffix(email, ".") || strings.Contains(email, "..") {
		return false
	}
	return true
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

// NewToken returns a fresh 256-bit token as lowercase hex. It is the value
// shown to a person or placed in a link; only its digest is ever stored.
func NewToken() string { return randomToken() }

// TokenHash is the SHA-256 digest stored for a token.
func TokenHash(token string) string { return tokenHash(token) }

type rateEntry struct {
	Count int
	Until time.Time
}

type limiter struct {
	mu      sync.Mutex
	entries map[string]rateEntry
}

func (l *limiter) allow(host string) bool {
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
