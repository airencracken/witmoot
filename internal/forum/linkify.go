package forum

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// bodySegment is a run of message text. When URL is empty the text is ordinary
// and the template escapes it; when URL is set the text is a bare web address.
type bodySegment struct {
	Text string
	URL  string
}

// segments splits plain message text so bare http(s) addresses can become
// links. Only http and https are ever recognised, and only whole tokens at a
// word boundary, so "javascript:" and "notahttps://x" stay ordinary text.
//
// The caller renders Text through the template, which escapes it, and URL in an
// href, where html/template applies its own URL filtering. Nothing here returns
// pre-rendered HTML, so a mistake in this function cannot inject markup.
func segments(text string) []bodySegment {
	var out []bodySegment
	last := 0
	for i := 0; i < len(text); i++ {
		if !startsURL(text, i) {
			continue
		}
		url, end := urlAt(text, i)
		if end == i {
			continue
		}
		if i > last {
			out = append(out, bodySegment{Text: text[last:i]})
		}
		out = append(out, bodySegment{Text: url, URL: url})
		last = end
		i = end - 1
	}
	if last < len(text) {
		out = append(out, bodySegment{Text: text[last:]})
	}
	return out
}

// startsURL reports whether a bare http(s) scheme begins at i and sits on a
// word boundary, so a scheme buried inside another token is left alone.
func startsURL(text string, i int) bool {
	if !hasPrefixFold(text[i:], "https://") && !hasPrefixFold(text[i:], "http://") {
		return false
	}
	if i == 0 {
		return true
	}
	previous, _ := utf8.DecodeLastRuneInString(text[:i])
	return !unicode.IsLetter(previous) && !unicode.IsDigit(previous)
}

// urlAt reads the address beginning at i and returns it with the index just
// past it. Trailing punctuation that belongs to the sentence, not the address,
// is left behind.
func urlAt(text string, i int) (string, int) {
	end := i
	for end < len(text) && !isURLTerminator(text[end]) {
		end++
	}
	token := trimURL(text[i:end])
	if !hasHost(token) {
		return "", i
	}
	return token, i + len(token)
}

// isURLTerminator stops a bare address at whitespace and at the characters that
// delimit one in ordinary prose or markup.
func isURLTerminator(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f', '<', '>', '"':
		return true
	}
	return false
}

// trimURL removes sentence punctuation and unbalanced closing brackets from the
// end of an address, so "see https://example.org/a(b)." links the address only.
func trimURL(token string) string {
	for {
		if trimmed := strings.TrimRight(token, ".,;:!?'"); trimmed != token {
			token = trimmed
			continue
		}
		switch {
		case strings.HasSuffix(token, ")") && strings.Count(token, ")") > strings.Count(token, "("):
			token = token[:len(token)-1]
		case strings.HasSuffix(token, "]") && strings.Count(token, "]") > strings.Count(token, "["):
			token = token[:len(token)-1]
		case strings.HasSuffix(token, "}") && strings.Count(token, "}") > strings.Count(token, "{"):
			token = token[:len(token)-1]
		default:
			return token
		}
	}
}

// hasHost requires something after the scheme and at least one letter or digit
// in the host, so "https://" and "http://." are not turned into links.
func hasHost(url string) bool {
	_, rest, ok := strings.Cut(url, "://")
	if !ok || rest == "" {
		return false
	}
	host := rest
	if idx := strings.IndexAny(rest, "/?#"); idx >= 0 {
		host = rest[:idx]
	}
	for _, r := range host {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// hasPrefixFold reports whether text begins with prefix, ignoring ASCII case,
// so an uppercase scheme is still recognised.
func hasPrefixFold(text, prefix string) bool {
	if len(text) < len(prefix) {
		return false
	}
	return strings.EqualFold(text[:len(prefix)], prefix)
}
