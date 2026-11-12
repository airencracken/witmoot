package forum

import (
	"errors"
	"github.com/airencracken/comfylib/ratelimit"
	"regexp"
	"strings"
	"time"
	"unicode"
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
// single bare address, which the mail sender refuses: whitespace, control
// characters, and the <, > and , that would make it a list or a display form.
func validEmail(email string) bool {
	if email == "" {
		return true
	}
	if len(email) > 254 || strings.ContainsAny(email, "<>,") {
		return false
	}
	for _, r := range email {
		if r <= ' ' || r == 0x7f {
			return false
		}
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

// SingleLine reports whether value can stand on one line of a page title or a
// mail header: it holds no control characters, which include CR, LF, tab and
// NEL, and no Unicode line or paragraph separators. Names that reach a mail
// subject, such as the site name, must pass, because the mail sender refuses a
// subject with a line break rather than rewriting it. Invalid UTF-8 fails too,
// since a reader could decode it as anything.
func SingleLine(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return false
		}
	}
	return true
}

// CheckSiteName validates a site name from the configuration, such as
// WITMOOT_NAME, before the server or an account command uses it.
func CheckSiteName(name string) error {
	if !SingleLine(name) || utf8.RuneCountInString(name) > 80 {
		return errors.New("WITMOOT_NAME must be one line of at most 80 characters")
	}
	return nil
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

const rateAttempts = 20
const rateClients = 10000

type limiter struct{ shared *ratelimit.Window }

func newLimiter() *limiter {
	return &limiter{shared: ratelimit.NewWindow(ratelimit.WindowConfig{Attempts: rateAttempts, Interval: 15 * time.Minute, Capacity: rateClients})}
}
func (l *limiter) allow(key string) bool { ok, _ := l.shared.Allow(key); return ok }
func (l *limiter) refund(key string)     { l.shared.Refund(key) }
