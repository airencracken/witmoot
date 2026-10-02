// SPDX-License-Identifier: AGPL-3.0-or-later

package scripts_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// comfylibModule finds the pinned comfylib in the module cache, the copy
// every build of this tree uses.
func comfylibModule(t *testing.T) (dir, version string) {
	t.Helper()
	cmd := exec.Command("go", "list", "-m", "-json", "github.com/airencracken/comfylib")
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -m -json github.com/airencracken/comfylib: %v", err)
	}
	var module struct{ Dir, Version string }
	if err := json.Unmarshal(out, &module); err != nil {
		t.Fatal(err)
	}
	if module.Dir == "" || module.Version == "" {
		t.Fatalf("comfylib is not in the module cache (%+v); run go mod download", module)
	}
	return module.Dir, module.Version
}

// syncHeader is the one line scripts/mutate.py adds to comfylib's engine.
var syncHeader = regexp.MustCompile(`^# Synced from github\.com/airencracken/comfylib@(\S+) tools/mutate\.py; edit it there\.\n`)

// The mutation engine is comfylib's, copied so the harness can run from a
// throwaway copy of this tree with GOPROXY=off. Apart from the line naming
// the release it came from, the copy must be byte-for-byte the pinned one.
func TestMutationEngineMatchesThePinnedComfylib(t *testing.T) {
	dir, version := comfylibModule(t)
	upstream, err := os.ReadFile(filepath.Join(dir, "tools", "mutate.py"))
	if err != nil {
		t.Fatal(err)
	}
	local, err := os.ReadFile("mutate.py")
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.SplitAfterN(local, []byte("\n"), 3)
	if len(lines) != 3 {
		t.Fatal("scripts/mutate.py is too short")
	}
	match := syncHeader.FindSubmatch(lines[2][:bytes.IndexByte(lines[2], '\n')+1])
	if match == nil {
		t.Fatal("scripts/mutate.py does not name the comfylib release it was copied from on its third line")
	}
	if string(match[1]) != version {
		t.Fatalf("scripts/mutate.py was copied from comfylib %s, but go.mod pins %s", match[1], version)
	}
	synced := append(append(append([]byte{}, lines[0]...), lines[1]...), lines[2][len(match[0]):]...)
	if !bytes.Equal(synced, upstream) {
		t.Fatalf("scripts/mutate.py differs from %s; copy it again and keep only the header line", filepath.Join(dir, "tools", "mutate.py"))
	}
}

// schema is the subset of JSON Schema that tools/mutation-schema.json uses.
type schema struct {
	Type                 string             `json:"type"`
	MinItems             int                `json:"minItems"`
	MinLength            int                `json:"minLength"`
	Pattern              string             `json:"pattern"`
	Required             []string           `json:"required"`
	Properties           map[string]*schema `json:"properties"`
	Items                *schema            `json:"items"`
	AdditionalProperties json.RawMessage    `json:"additionalProperties"`
}

// validate reports every way value breaks s, with where naming the value.
func (s *schema) validate(value any, where string) []string {
	var problems []string
	switch s.Type {
	case "array":
		list, ok := value.([]any)
		if !ok {
			return []string{where + ": not an array"}
		}
		if len(list) < s.MinItems {
			problems = append(problems, fmt.Sprintf("%s: %d items, want at least %d", where, len(list), s.MinItems))
		}
		for i, item := range list {
			problems = append(problems, s.Items.validate(item, fmt.Sprintf("%s[%d]", where, i))...)
		}
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return []string{where + ": not an object"}
		}
		problems = append(problems, s.validateObject(object, where)...)
	case "string":
		text, ok := value.(string)
		if !ok {
			return []string{where + ": not a string"}
		}
		if len([]rune(text)) < s.MinLength {
			problems = append(problems, where+": too short")
		}
		if s.Pattern != "" && !regexp.MustCompile(s.Pattern).MatchString(text) {
			problems = append(problems, fmt.Sprintf("%s: %q does not match %s", where, text, s.Pattern))
		}
	default:
		problems = append(problems, where+": the schema uses unsupported type "+s.Type)
	}
	return problems
}

func (s *schema) validateObject(object map[string]any, where string) []string {
	var problems []string
	for _, key := range s.Required {
		if _, ok := object[key]; !ok {
			problems = append(problems, where+": missing "+key)
		}
	}
	var extra *schema
	closed := string(s.AdditionalProperties) == "false"
	if len(s.AdditionalProperties) > 0 && !closed {
		extra = new(schema)
		if err := json.Unmarshal(s.AdditionalProperties, extra); err != nil {
			problems = append(problems, where+": unreadable additionalProperties")
		}
	}
	for key, child := range object {
		switch property := s.Properties[key]; {
		case property != nil:
			problems = append(problems, property.validate(child, where+"."+key)...)
		case closed:
			problems = append(problems, where+": unknown field "+key)
		case extra != nil:
			problems = append(problems, extra.validate(child, where+"."+key)...)
		}
	}
	return problems
}

func mutationSchema(t *testing.T) *schema {
	t.Helper()
	dir, _ := comfylibModule(t)
	data, err := os.ReadFile(filepath.Join(dir, "tools", "mutation-schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s schema
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	return &s
}

func mutationTables(t *testing.T) []string {
	t.Helper()
	tables, err := filepath.Glob("mutations/*.json")
	if err != nil || len(tables) == 0 {
		t.Fatalf("no mutation tables: %v", err)
	}
	return tables
}

// Every table follows comfylib's schema, and every table runs from a Make
// target, so none is silently skipped.
func TestMutationTablesMatchTheSchema(t *testing.T) {
	s := mutationSchema(t)
	makefile, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range mutationTables(t) {
		data, err := os.ReadFile(table)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		for _, problem := range s.validate(value, table) {
			t.Error(problem)
		}
		if !bytes.Contains(makefile, []byte("scripts/"+table)) {
			t.Errorf("no Make target runs %s", table)
		}
	}
}

// The schema check itself must refuse what the engine would refuse.
func TestMutationSchemaRejectsMalformedEntries(t *testing.T) {
	s := mutationSchema(t)
	valid := func() map[string]any {
		return map[string]any{"name": "n", "file": "internal/forum/web.go", "before": "a", "after": "b", "package": "./internal/forum", "run": "^TestX$"}
	}
	if problems := s.validate([]any{valid()}, "valid"); len(problems) != 0 {
		t.Fatalf("a valid entry was refused: %v", problems)
	}
	for name, change := range map[string]func(map[string]any){
		"unknown field":    func(e map[string]any) { e["comment"] = "x" },
		"missing run":      func(e map[string]any) { delete(e, "run") },
		"empty before":     func(e map[string]any) { e["before"] = "" },
		"absolute file":    func(e map[string]any) { e["file"] = "/etc/passwd" },
		"file with spaces": func(e map[string]any) { e["file"] = "a b.go" },
		"bare package":     func(e map[string]any) { e["package"] = "internal/forum" },
		"package spaces":   func(e map[string]any) { e["package"] = "./internal/forum ./cmd/witmoot" },
		"numeric after":    func(e map[string]any) { e["after"] = 1.0 },
		"env not strings":  func(e map[string]any) { e["env"] = map[string]any{"X": true} },
		"env not object":   func(e map[string]any) { e["env"] = []any{"X=1"} },
	} {
		entry := valid()
		change(entry)
		if problems := s.validate([]any{entry}, name); len(problems) == 0 {
			t.Errorf("%s was accepted", name)
		}
	}
	if problems := s.validate([]any{}, "empty"); len(problems) == 0 {
		t.Error("an empty table was accepted")
	}
}

// Each mutation names tests that exist in its package, and each anchor still
// occurs exactly once, which the engine checks when it loads the tables.
func TestMutationTablesTargetExistingCode(t *testing.T) {
	for _, table := range mutationTables(t) {
		data, err := os.ReadFile(table)
		if err != nil {
			t.Fatal(err)
		}
		var entries []struct{ Name, File, Package, Run string }
		if err := json.Unmarshal(data, &entries); err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			sources, err := filepath.Glob(filepath.Join("..", entry.Package, "*_test.go"))
			if err != nil || len(sources) == 0 {
				t.Errorf("%s: package %s has no tests", entry.Name, entry.Package)
				continue
			}
			var tests []string
			for _, source := range sources {
				text, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				for _, match := range regexp.MustCompile(`(?m)^func (Test\w+)\(t \*testing\.T\)`).FindAllSubmatch(text, -1) {
					tests = append(tests, string(match[1]))
				}
			}
			names := strings.Split(strings.Trim(strings.TrimSuffix(strings.TrimPrefix(entry.Run, "^"), "$"), "()"), "|")
			for _, name := range names {
				if !slices.Contains(tests, name) {
					t.Errorf("%s: %s has no test %s", entry.Name, entry.Package, name)
				}
			}
		}
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("needs python3 to check the anchors")
	}
	cmd := exec.Command(python, append([]string{"mutate.py", "--root", "..", "--list"}, mutationTables(t)...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the engine refused the tables: %v\n%s", err, out)
	}
}
