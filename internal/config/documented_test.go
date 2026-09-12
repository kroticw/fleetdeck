package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The configuration page opens by promising that it lists every key this
// package recognizes, what each one defaults to, and what happens when a
// value is wrong. A promise of completeness is worse than no promise when it
// is false: a reader who does not find a key in a list that claims to be
// closed concludes the key is invented or withdrawn, and may delete it from
// a working configuration -- which raises no error, because the key is real
// and the panel simply stops doing what it did.
//
// The promise is held here rather than by review. It went false once already
// without anyone noticing (session_labels, statusline.wrap and
// statusline.rate_limits_path were declared and never written down), and the
// only CI job that looked at documentation at all compared the two languages
// with each other -- which passes when both are missing the same key.

// keyTableMarker precedes the table both configuration.md files keep the
// promise in. The table is found by this comment and not by its heading,
// which is translated: an anchor that has to be kept in step with prose in
// two languages is one more thing to get wrong. The comment is in the
// documents themselves so that whoever edits the table can see it is
// checked, and it says so there in words -- only this prefix is matched, so
// the rest of that HTML comment is free to explain itself.
const keyTableMarker = "<!-- fleetdeck:config-keys"

// keyTableColumns is the table's shape: the YAML path, the type, the default,
// and what a wrong value breaks -- the three things after the path being
// exactly what the page's first paragraph promises for every key.
const keyTableColumns = 4

// declaredKeys walks the yaml tags of the configuration file's own shape and
// returns every path a file may set, in dotted form (`notify.silence_after`).
//
// Two kinds of field are not leaves:
//
//   - a struct is a container and never a key of its own. `board:` with no
//     `path:` under it sets nothing and has no default, so `board` is not a
//     row; `board.path` is.
//   - a list of structs is both. The list itself is a value -- it has a
//     default (unset) and its own wrong-value behavior -- and it has keys
//     underneath, which are written with an index-free `[]` because the
//     entries are alike and the page describes them once.
func declaredKeys(typ reflect.Type, prefix string) []string {
	var keys []string
	for i := range typ.NumField() {
		field := typ.Field(i)
		tag, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		path := tag
		if prefix != "" {
			path = prefix + "." + tag
		}
		switch ft := field.Type; {
		case ft.Kind() == reflect.Struct:
			keys = append(keys, declaredKeys(ft, path)...)
		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
			keys = append(keys, path)
			keys = append(keys, declaredKeys(ft.Elem(), path+"[]")...)
		default:
			keys = append(keys, path)
		}
	}
	return keys
}

// tableCells splits one markdown table row into its trimmed cells.
func tableCells(row string) []string {
	row = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(row), "|"), "|")
	cells := strings.Split(row, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// documentedKeys reads the key table out of docs/<lang>/configuration.md and
// returns the paths it lists. A row whose path is not in backticks is the
// header or the separator, and is skipped; every other row is a key and is
// required to carry all four columns filled, since a row with an empty
// default or an empty wrong-value cell keeps the list complete while leaving
// the rest of the same sentence's promise unmet.
func documentedKeys(t *testing.T, lang string) []string {
	t.Helper()
	path := filepath.Join("..", "..", "docs", lang, "configuration.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, after, found := strings.Cut(string(raw), keyTableMarker)
	if !found {
		t.Fatalf("%s does not carry %s, so the key table cannot be found; put the comment on the line above the table", path, keyTableMarker)
	}
	var keys []string
	inTable := false
	for _, line := range strings.Split(after, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			if inTable {
				break
			}
			continue
		}
		inTable = true
		cells := tableCells(line)
		if len(cells) == 0 || !strings.HasPrefix(cells[0], "`") {
			continue
		}
		key := strings.Trim(cells[0], "`")
		keys = append(keys, key)
		if len(cells) != keyTableColumns {
			t.Errorf("%s: row for %s has %d columns, want %d (path, type, default, what breaks)", path, key, len(cells), keyTableColumns)
			continue
		}
		for i, cell := range cells {
			if cell == "" {
				t.Errorf("%s: row for %s leaves column %d empty; the page promises a default and a wrong-value outcome for every key", path, key, i+1)
			}
		}
	}
	if !inTable {
		t.Fatalf("%s: no table follows %s", path, keyTableMarker)
	}
	return keys
}

// TestConfigurationPageListsEveryKey holds the promise configuration.md opens
// with, in both languages, against the shape this package actually decodes.
// It fails in both directions: a key the code recognizes and the page does
// not is the promise broken, and a key the page describes and the code does
// not is the page sending a reader to write something the loader will reject
// as unknown.
func TestConfigurationPageListsEveryKey(t *testing.T) {
	declared := declaredKeys(reflect.TypeOf(file{}), "")
	slices.Sort(declared)
	if len(declared) == 0 {
		t.Fatal("no keys were read off the configuration file's own shape; declaredKeys is broken, not the documentation")
	}
	for _, lang := range []string{"en", "ru"} {
		documented := documentedKeys(t, lang)
		slices.Sort(documented)
		for _, key := range declared {
			if !slices.Contains(documented, key) {
				t.Errorf("docs/%s/configuration.md does not list %s, which the configuration recognizes", lang, key)
			}
		}
		for _, key := range documented {
			if !slices.Contains(declared, key) {
				t.Errorf("docs/%s/configuration.md lists %s, which the configuration does not recognize and Load rejects as an unknown key", lang, key)
			}
		}
	}
}

// The two languages describe the same keys. docs-parity in CI compares their
// headings, which stays green when a key is missing from both -- exactly how
// the three undocumented keys survived.
func TestBothLanguagesListTheSameKeys(t *testing.T) {
	en, ru := documentedKeys(t, "en"), documentedKeys(t, "ru")
	slices.Sort(en)
	slices.Sort(ru)
	if !slices.Equal(en, ru) {
		t.Errorf("the key tables differ:\n en: %v\n ru: %v", en, ru)
	}
}
