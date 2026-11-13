// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// lowercaseProduct finds a product name written in lowercase, skipping the
// identifiers, URLs, and paths where lowercase is correct: a name touching a
// slash, dash, underscore, colon, another word character, or a dot inside a
// file or host name is left alone.
var lowercaseProduct = regexp.MustCompile(`(?:^|[^/\w.\-:@])(imvault|witmoot|comfyware|songstead)(?:$|[^/\w.\-:]|\.(?:$|\s))`)

func lowercaseProducts(text string) []string {
	var found []string
	for _, match := range lowercaseProduct.FindAllStringSubmatch(text, -1) {
		found = append(found, match[1])
	}
	return found
}

func TestLowercaseProductMatcher(t *testing.T) {
	for text, want := range map[string]int{
		"Your imvault library":             1,
		"Open witmoot.":                    1,
		"imvault":                          1,
		"Ask Imvault and Witmoot":          0,
		"/account/imvault":                 0,
		"https://imvault.example.org/f/1":  0,
		"witmoot-community-export":         0,
		"imvault.key":                      0,
		"WITMOOT_ADDR":                     0,
		"run witmoot_migrate":              0,
		"data-comfy-app=witmoot-x":         0,
		"witmoot:8080":                     0,
		"songstead and comfyware together": 2,
	} {
		if got := len(lowercaseProducts(text)); got != want {
			t.Errorf("%q: found %d, want %d", text, got, want)
		}
	}
}

// Visible template text and the attributes people read must write product
// names capitalised.
func TestTemplatesCapitaliseProductNames(t *testing.T) {
	files, err := filepath.Glob("templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	actions := regexp.MustCompile(`(?s){{.*?}}`)
	code := regexp.MustCompile(`(?s)<code>.*?</code>`)
	tags := regexp.MustCompile(`(?s)<[^>]*>`)
	readable := regexp.MustCompile(`\b(?:placeholder|aria-label|title|alt)="([^"]*)"`)
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		page := actions.ReplaceAllString(string(content), " ")
		for _, attribute := range readable.FindAllStringSubmatch(page, -1) {
			if names := lowercaseProducts(attribute[1]); names != nil {
				t.Errorf("%s: attribute %q writes %v in lowercase", file, attribute[0], names)
			}
		}
		text := tags.ReplaceAllString(code.ReplaceAllString(page, " "), " ")
		for _, line := range strings.Split(text, "\n") {
			if names := lowercaseProducts(line); names != nil {
				t.Errorf("%s: text %q writes %v in lowercase", file, strings.TrimSpace(line), names)
			}
		}
	}
}

// Strings written as interface copy start with a capital and contain a space.
// Those must capitalise product names too.
func TestUserFacingStringsCapitaliseProductNames(t *testing.T) {
	for _, dir := range []string{".", "../imvault"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		checked := 0
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(literal.Value)
				if err != nil || value == "" || !strings.Contains(value, " ") || value[0] < 'A' || value[0] > 'Z' {
					return true
				}
				checked++
				if names := lowercaseProducts(value); names != nil {
					t.Errorf("%s: %q writes %v in lowercase", file, value, names)
				}
				return true
			})
		}
		if checked == 0 {
			t.Fatalf("no user-facing strings found in %s", dir)
		}
	}
}
