package forum

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestSegmentsFindBareAddresses(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want []bodySegment
	}{
		{
			name: "sentence punctuation stays outside the link",
			text: "See https://example.org/foo for details.",
			want: []bodySegment{{Text: "See "}, {Text: "https://example.org/foo", URL: "https://example.org/foo"}, {Text: " for details."}},
		},
		{
			name: "trailing punctuation is trimmed",
			text: "https://example.org/a.,;:!?",
			want: []bodySegment{{Text: "https://example.org/a", URL: "https://example.org/a"}, {Text: ".,;:!?"}},
		},
		{
			name: "unbalanced closing bracket is trimmed",
			text: "(https://en.wikipedia.org/wiki/Foo_(bar))",
			want: []bodySegment{{Text: "("}, {Text: "https://en.wikipedia.org/wiki/Foo_(bar)", URL: "https://en.wikipedia.org/wiki/Foo_(bar)"}, {Text: ")"}},
		},
		{
			name: "a scheme inside a word is not a link",
			text: "notahttps://example.org and https://example.org",
			want: []bodySegment{{Text: "notahttps://example.org and "}, {Text: "https://example.org", URL: "https://example.org"}},
		},
		{
			name: "other schemes are left alone",
			text: "javascript:alert(1) mailto:a@b.example",
			want: []bodySegment{{Text: "javascript:alert(1) mailto:a@b.example"}},
		},
		{
			name: "a bare scheme is not a link",
			text: "https:// is not enough",
			want: []bodySegment{{Text: "https:// is not enough"}},
		},
		{
			name: "uppercase scheme still links",
			text: "HTTPS://Example.ORG/path",
			want: []bodySegment{{Text: "HTTPS://Example.ORG/path", URL: "HTTPS://Example.ORG/path"}},
		},
		{
			name: "line breaks are preserved between links",
			text: "https://one.example\nhttps://two.example",
			want: []bodySegment{
				{Text: "https://one.example", URL: "https://one.example"},
				{Text: "\n"},
				{Text: "https://two.example", URL: "https://two.example"},
			},
		},
		{
			name: "query strings keep their ampersands",
			text: "https://example.org/?a=1&b=2",
			want: []bodySegment{{Text: "https://example.org/?a=1&b=2", URL: "https://example.org/?a=1&b=2"}},
		},
		{
			name: "quotes end an address",
			text: `a "https://example.org/x" b`,
			want: []bodySegment{{Text: `a "`}, {Text: "https://example.org/x", URL: "https://example.org/x"}, {Text: `" b`}},
		},
		{
			name: "no address is one text run",
			text: "just a note",
			want: []bodySegment{{Text: "just a note"}},
		},
		{name: "empty text", text: "", want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := segments(tc.text); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("segments(%q) =\n%#v\nwant\n%#v", tc.text, got, tc.want)
			}
		})
	}
}

func TestPostBodyRendersLinksAndEscapesEverythingElse(t *testing.T) {
	app, client := newTestApp(t, false)
	user := signInTest(t, app, client, false)
	id, err := app.store.CreateTopic(t.Context(), 1, user, "A recipe",
		"Try https://example.org/recipe?a=1&b=2 and <script>alert(1)</script>", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	w := client.request("GET", "/topics/"+strconv.FormatInt(id, 10), nil, nil)
	requireStatus(t, w, 200)
	body := w.Body.String()
	if !strings.Contains(body, `<a href="https://example.org/recipe?a=1&amp;b=2" rel="noopener noreferrer external">`) {
		t.Fatal("a bare address was not turned into an escaped link")
	}
	if strings.Contains(body, "<script>alert") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("surrounding text is not escaped")
	}
}

func TestPostBodyDoesNotLinkOtherSchemes(t *testing.T) {
	app, client := newTestApp(t, false)
	user := signInTest(t, app, client, false)
	id, err := app.store.CreateTopic(t.Context(), 1, user, "Careful", "javascript:alert(1)", AudienceMembers)
	if err != nil {
		t.Fatal(err)
	}
	w := client.request("GET", "/topics/"+strconv.FormatInt(id, 10), nil, nil)
	requireStatus(t, w, 200)
	if strings.Contains(w.Body.String(), `<a href="javascript`) || strings.Contains(w.Body.String(), "<a href=\"#ZgotmplZ\"") {
		t.Fatal("a non-http scheme became a link")
	}
}
