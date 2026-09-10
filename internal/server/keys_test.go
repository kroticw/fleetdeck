package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/kroticw/fleetdeck/web"
)

// The key buttons are the most expensive thing in this repository to get wrong.
// `keys` is written into a live session's terminal byte for byte — handleSendKeys
// hands it to daemon.Client.SendKeys, which does conn.Write([]byte(keys)) — so a
// button that sent its own name would type the letters e, s, c, a, p, e into
// whatever an agent was doing, mid-sentence, in a session nobody was watching.
//
// A test that asked some lookup table for "escape" and checked it answered \x1b
// would prove nothing: it would pass just as well if the panel never consulted
// the table. So this reads the table out of web/js/session.js as it actually
// ships — from web.FS, the same embedded filesystem the browser is served from —
// and drives every entry through the real route with the real guard, asserting
// on what the daemon is handed at the other end.
//
// The two halves meet here. web/tests/session.test.js proves that clicking a
// button produces this payload; this proves that this payload reaches the daemon
// as terminal bytes. Neither half can be true while the panel types words into a
// session.

// keyEntry matches one row of the KEYS table in web/js/session.js:
//
//	{ id: "escape", label: "Esc", bytes: "\u001b" },
var keyEntry = regexp.MustCompile(`\{\s*id:\s*"([^"]*)",\s*label:\s*"((?:[^"\\]|\\.)*)",\s*bytes:\s*"((?:[^"\\]|\\.)*)"\s*\}`)

type panelKey struct {
	id    string
	label string
	bytes string
}

// panelKeys reads the panel's key table out of the embedded frontend and decodes
// its JavaScript string escapes. Go and JavaScript agree on \uXXXX and \r, so
// strconv.Unquote is the decoder; anything it cannot read is a failure rather
// than a row quietly skipped.
func panelKeys(t *testing.T) []panelKey {
	t.Helper()

	source, err := web.FS.ReadFile("js/session.js")
	if err != nil {
		t.Fatalf("read the panel's source from the embedded frontend: %v", err)
	}

	table := regexp.MustCompile(`(?s)export const KEYS = \[(.*?)\n\];`).FindSubmatch(source)
	if table == nil {
		t.Fatal("web/js/session.js no longer declares `export const KEYS = [...]`: this test can no longer see what the panel sends, which is not the same as the panel being correct")
	}

	matches := keyEntry.FindAllStringSubmatch(string(table[1]), -1)
	if len(matches) == 0 {
		t.Fatal("the panel's KEYS table parsed as empty: every assertion below would pass over nothing")
	}

	keys := make([]panelKey, 0, len(matches))
	for _, m := range matches {
		decoded, err := strconv.Unquote(`"` + m[3] + `"`)
		if err != nil {
			t.Fatalf("key %q sends %q, which is not a string this test can decode: %v", m[1], m[3], err)
		}
		keys = append(keys, panelKey{id: m[1], label: m[2], bytes: decoded})
	}
	return keys
}

func TestPanelKeyButtonsDeliverTerminalBytesToTheDaemon(t *testing.T) {
	keys := panelKeys(t)

	// Every button on the panel, and no fewer: a table that lost a row would
	// otherwise shrink this test rather than fail it.
	if len(keys) < 4 {
		t.Fatalf("the panel offers %d key buttons; it had four (Esc, up, down, Enter) and losing one silently is exactly what this count is for", len(keys))
	}

	for _, key := range keys {
		t.Run(key.id, func(t *testing.T) {
			d, _ := testDeps()
			var delivered []string
			d.SendKeys = func(_, keys string) error {
				delivered = append(delivered, keys)
				return nil
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/sessions/abc123/keys", strings.NewReader(keysBody(t, key.bytes)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", "http://127.0.0.1:7777")
			New(d).ServeHTTP(rec, req)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("want 204 from the keys route, got %d: %s", rec.Code, rec.Body.String())
			}
			if len(delivered) != 1 {
				t.Fatalf("the daemon was handed %d writes, want exactly 1", len(delivered))
			}
			got := delivered[0]

			if got != key.bytes {
				t.Fatalf("the daemon received %q, but the panel sends %q: something between the button and the terminal is rewriting it", got, key.bytes)
			}

			// The assertion that matters. What arrives has to be terminal
			// control bytes — an escape sequence or a carriage return — and
			// never the word printed on the button or the identifier behind it.
			if !hasControlByte(got) {
				t.Errorf("the %s button delivers %q, which is ordinary text: a session would receive it as typing, not as a key", key.id, got)
			}
			if got == key.id {
				t.Errorf("the %s button delivers its own identifier, %q, which a session would type out letter by letter", key.id, got)
			}
			if got == key.label {
				t.Errorf("the %s button delivers its own label, %q, which a session would type out letter by letter", key.id, got)
			}
		})
	}
}

// keysBody builds the request body the panel builds: JSON.stringify of a
// {keys} object. Marshalled rather than formatted, because Go's %q escapes an
// ESC as \x1b and JSON has no \x escape at all — a body written that way is
// refused before it reaches a handler, which is how this test first failed.
func keysBody(t *testing.T, keys string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"keys": keys})
	if err != nil {
		t.Fatalf("marshal the request body: %v", err)
	}
	return string(body)
}

// hasControlByte reports whether s carries at least one C0 control character —
// ESC, CR and their neighbours. Every key this panel offers is one of those,
// alone or at the head of a sequence.
func hasControlByte(s string) bool {
	for _, r := range s {
		if r < unicode.MaxASCII && unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// TestKeysRouteDeliversBytesUntouched is the other half of the same guarantee,
// from the server's side: whatever the panel sends is what the daemon gets. The
// route deliberately performs no translation of its own — `keys` is raw terminal
// input, and a server that helpfully rewrote it could never be asked to send a
// literal string again.
func TestKeysRouteDeliversBytesUntouched(t *testing.T) {
	for _, payload := range []string{"\x1b", "\x1b[A", "\r", "escape", "hello world", "\x1b[31mred\x1b[0m"} {
		t.Run(strconv.Quote(payload), func(t *testing.T) {
			d, _ := testDeps()
			var got string
			d.SendKeys = func(_, keys string) error {
				got = keys
				return nil
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/sessions/abc123/keys", strings.NewReader(keysBody(t, payload)))
			req.Header.Set("Content-Type", "application/json")
			New(d).ServeHTTP(rec, req)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
			}
			if got != payload {
				t.Fatalf("sent %q, the daemon received %q", payload, got)
			}
		})
	}
}
