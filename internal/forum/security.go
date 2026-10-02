package forum

import (
	"container/list"
	"errors"
	"net/netip"
	"regexp"
	"strings"
	"sync"
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

const (
	rateWindow   = 15 * time.Minute
	rateAttempts = 20
	// rateClients bounds the limiter's memory. When it is full the oldest
	// record is dropped, so a flood of addresses cannot lock out new visitors.
	rateClients = 10000
)

type rateEntry struct {
	key   string
	count int
	until time.Time
}

// limiter budgets authentication attempts per client network. Records are
// kept in creation order, which is also expiry order because every window has
// the same length, so pruning and eviction never scan the whole table.
type limiter struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   list.List
}

func newLimiter() *limiter { return &limiter{entries: make(map[string]*list.Element)} }

// rateKey groups IPv6 clients by /64, the smallest block normally assigned to
// one site, so one host cannot claim a fresh budget per address.
func rateKey(client string) string {
	addr, err := netip.ParseAddr(client)
	if err != nil {
		return client
	}
	addr = addr.Unmap()
	if addr.Is6() {
		return netip.PrefixFrom(addr.WithZone(""), 64).Masked().String()
	}
	return addr.String()
}

// allow spends one attempt from the client's budget.
func (l *limiter) allow(client string) bool {
	key := rateKey(client)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for front := l.order.Front(); front != nil && !front.Value.(*rateEntry).until.After(now); front = l.order.Front() {
		l.remove(front)
	}
	element, ok := l.entries[key]
	if !ok {
		for l.order.Len() >= rateClients {
			l.remove(l.order.Front())
		}
		element = l.order.PushBack(&rateEntry{key: key, until: now.Add(rateWindow)})
		l.entries[key] = element
	}
	entry := element.Value.(*rateEntry)
	if entry.count >= rateAttempts {
		return false
	}
	entry.count++
	return true
}

// refund returns an attempt that succeeded, so people who sign in often are
// never locked out by their own correct passwords.
func (l *limiter) refund(client string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if element, ok := l.entries[rateKey(client)]; ok {
		if entry := element.Value.(*rateEntry); entry.count > 0 {
			entry.count--
		}
	}
}

func (l *limiter) remove(element *list.Element) {
	delete(l.entries, element.Value.(*rateEntry).key)
	l.order.Remove(element)
}
