package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A real, minimal PNG: the signature plus the chunks a decoder needs. The bytes
// matter — this route decides what a file is by looking at them, never by what
// the request claimed.
var pngBytes = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde,
	0x00, 0x00, 0x00, 0x0c, 'I', 'D', 'A', 'T',
	0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00, 0x00, 0x03, 0x01, 0x01, 0x00,
	0x18, 0xdd, 0x8d, 0xb0,
	0x00, 0x00, 0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

// JPEG's own signature. Enough for content sniffing, which is all this route
// does with it.
var jpegBytes = append([]byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00},
	make([]byte, 64)...)

var gifBytes = append([]byte("GIF89a"), make([]byte, 64)...)

func imageDeps(t *testing.T) (Deps, string) {
	t.Helper()
	d, _ := testDeps()
	dir := t.TempDir()
	d.ImageDir = dir
	return d, dir
}

// workingDirEntries is the package directory's contents as a set, so a test can
// say what a request created rather than what happens to be lying there.
func workingDirEntries(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read working directory: %v", err)
	}
	names := make(map[string]bool, len(entries))
	for _, entry := range entries {
		names[entry.Name()] = true
	}
	return names
}

func imageBody(raw []byte) string {
	return fmt.Sprintf(`{"data":%q}`, base64.StdEncoding.EncodeToString(raw))
}

// savedPath pulls the path out of a successful response, failing the test when
// the answer is not the shape the panel is promised.
func savedPath(t *testing.T, body string) string {
	t.Helper()
	var got struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if got.Path == "" {
		t.Fatalf("the response must name the file it wrote: %s", body)
	}
	return got.Path
}

func TestImageIsWrittenVerbatim(t *testing.T) {
	d, dir := imageDeps(t)

	rec := do(d, http.MethodPost, "/api/sessions/abc123/image", imageBody(pngBytes))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	path := savedPath(t, rec.Body.String())

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back %s: %v", path, err)
	}
	if string(written) != string(pngBytes) {
		t.Fatalf("the file on disk is not the image that was sent")
	}
	if !strings.HasPrefix(path, dir+string(filepath.Separator)) {
		t.Fatalf("the file must land inside the configured image directory, got %q", path)
	}
}

// The session id decides the subdirectory, so a session's images stay together
// and one session's upload cannot be mistaken for another's.
func TestImageLandsUnderItsSession(t *testing.T) {
	d, dir := imageDeps(t)

	rec := do(d, http.MethodPost, "/api/sessions/abc123/image", imageBody(pngBytes))
	path := savedPath(t, rec.Body.String())

	want := filepath.Join(dir, "abc123")
	if filepath.Dir(path) != want {
		t.Fatalf("want the file under %q, got %q", want, path)
	}
}

// Spec section 8's rule, applied to the one place this route builds a path from
// something the caller sent. A session id that climbs out of the directory must
// be refused before anything is created, not sanitised into something plausible.
// Worth knowing what each case actually exercises, because the router does not
// hand them all to the handler. "/api/sessions/../image" and ".../a/b/image"
// never match the pattern at all — a wildcard does not span a separator — so
// those two prove the router refuses them, not the allowlist.
//
// The two that do reach the handler are the ones that matter. ".hidden" arrives
// as written and is refused because a dot is not in the allowlist. "a%2fb"
// arrives decoded, as "a/b" with a real separator in a single path element, and
// is refused because a separator is not in the allowlist either — it is here
// without any dots in it deliberately: "a%2f..%2fb" would be refused for its
// dots even if separators were allowed, so it cannot tell the two rules apart.
func TestImageRefusesSessionIdThatClimbsOut(t *testing.T) {
	for _, id := range []string{"..", "../evil", "a%2f..%2fb", "a%2fb", ".hidden", "a/b"} {
		t.Run(id, func(t *testing.T) {
			d, dir := imageDeps(t)

			rec := do(d, http.MethodPost, "/api/sessions/"+id+"/image", imageBody(pngBytes))
			if rec.Code == http.StatusOK {
				t.Fatalf("a session id of %q must not be accepted: %s", id, rec.Body.String())
			}

			// Nothing may have been created anywhere, including inside the
			// directory: a refusal that still wrote a file is not a refusal.
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("read dir: %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("a refused request created %d entries", len(entries))
			}
		})
	}
}

// The file's name is the server's to choose. Nothing from the request reaches
// it, so there is nothing in it to escape with.
func TestImageNameComesFromTheServer(t *testing.T) {
	d, _ := imageDeps(t)

	// An unknown field is refused outright, which is what stops a "name" or
	// "filename" key from ever being read, let alone used.
	rec := do(d, http.MethodPost, "/api/sessions/abc123/image",
		fmt.Sprintf(`{"data":%q,"name":"../../etc/passwd"}`,
			base64.StdEncoding.EncodeToString(pngBytes)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a request naming the file must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Two uploads, two files: a name that repeated would silently replace the image
// the operator sent a moment ago.
func TestImagesDoNotOverwriteEachOther(t *testing.T) {
	d, _ := imageDeps(t)

	first := savedPath(t, do(d, http.MethodPost, "/api/sessions/abc123/image", imageBody(pngBytes)).Body.String())
	second := savedPath(t, do(d, http.MethodPost, "/api/sessions/abc123/image", imageBody(pngBytes)).Body.String())

	if first == second {
		t.Fatalf("two uploads produced the same path %q", first)
	}
	for _, path := range []string{first, second} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
	}
}

// The extension follows the bytes, not a claim: a file whose name says .png
// while its content is a JPEG teaches every later reader the wrong thing.
func TestImageExtensionFollowsTheBytes(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		ext  string
	}{
		{"png", pngBytes, ".png"},
		{"jpeg", jpegBytes, ".jpg"},
		{"gif", gifBytes, ".gif"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := imageDeps(t)
			rec := do(d, http.MethodPost, "/api/sessions/abc123/image", imageBody(tc.raw))
			if rec.Code != http.StatusOK {
				t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
			}
			path := savedPath(t, rec.Body.String())
			if filepath.Ext(path) != tc.ext {
				t.Fatalf("want extension %q, got %q", tc.ext, path)
			}
		})
	}
}

// The whole point of sniffing: something that is not an image is refused even
// when everything about the request says it is one.
func TestImageRefusesWhatIsNotAnImage(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"a shell script", []byte("#!/bin/sh\nrm -rf /\n")},
		{"html", []byte("<html><script>alert(1)</script></html>")},
		{"a mach-o header", []byte{0xcf, 0xfa, 0xed, 0xfe, 0x0c, 0x00, 0x00, 0x01}},
		{"a png signature with nothing after it", []byte{0x89, 'P', 'N', 'G'}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, dir := imageDeps(t)
			rec := do(d, http.MethodPost, "/api/sessions/abc123/image", imageBody(tc.raw))
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("want 415, got %d: %s", rec.Code, rec.Body.String())
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatalf("a refused upload left %d entries behind", len(entries))
			}
		})
	}
}

// An announced ceiling, enforced on the decoded bytes rather than on the
// base64, because the decoded size is what lands on the disk.
func TestImageRefusesWhatIsTooLarge(t *testing.T) {
	d, dir := imageDeps(t)

	oversized := append(append([]byte{}, pngBytes...), make([]byte, maxImageBytes)...)
	rec := do(d, http.MethodPost, "/api/sessions/abc123/image", imageBody(oversized))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), fmt.Sprint(maxImageBytes)) {
		t.Fatalf("the refusal must state the limit it enforces: %s", rec.Body.String())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("an oversized upload left %d entries behind", len(entries))
	}
}

// The two ceilings must not collapse into one. Base64 costs a third, so a body
// ceiling set just above base64(maxImageBytes) leaves almost no room in which an
// oversized image is refused as an image: everything past it is cut off by the
// body reader, whose message is about request bytes and names the wrong limit.
//
// Pinned as arithmetic rather than as a message, so a later change to either
// constant fails here instead of quietly moving every oversized upload onto the
// less useful refusal.
func TestTheImageCeilingIsReachableBeforeTheBodyCeiling(t *testing.T) {
	// What an image one byte past the limit costs on the wire, base64 plus the
	// small JSON envelope around it.
	encoded := int64(base64.StdEncoding.EncodedLen(maxImageBytes+1)) + 32
	if encoded >= maxImageBodyBytes {
		t.Fatalf("an image just past the %d-byte ceiling encodes to %d bytes, which the "+
			"%d-byte body ceiling already refuses: the image limit can never report itself",
			maxImageBytes, encoded, maxImageBodyBytes)
	}

	// And a real margin, not one byte of it: an image a quarter again over the
	// limit must still be read far enough to be refused for its own size.
	generous := int64(base64.StdEncoding.EncodedLen(maxImageBytes*5/4)) + 32
	if generous >= maxImageBodyBytes {
		t.Fatalf("only %d bytes of headroom above base64(maxImageBytes): the window in which "+
			"an oversized image reports itself is too narrow to be useful",
			maxImageBodyBytes-int64(base64.StdEncoding.EncodedLen(maxImageBytes)))
	}
}

// The other ceiling: the request body itself, enforced while reading rather
// than after decoding, so a caller cannot make the panel buffer an unbounded
// stream. Its refusal must name the limit that actually stopped it — this route
// and the rest of the API no longer share one, and a message naming the other
// constant sends the reader to the wrong place.
func TestImageRefusesABodyBeyondTheReadCeiling(t *testing.T) {
	d, dir := imageDeps(t)

	// Bigger than the body ceiling, so the read is cut off before any decoding
	// happens and the decoded-size check below it is never reached.
	huge := strings.Repeat("A", maxImageBodyBytes+1024)
	rec := do(d, http.MethodPost, "/api/sessions/abc123/image", `{"data":"`+huge+`"}`)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), fmt.Sprint(maxImageBodyBytes)) {
		t.Fatalf("the refusal must name the ceiling that stopped it (%d): %s",
			maxImageBodyBytes, rec.Body.String())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("an oversized body left %d entries behind", len(entries))
	}
}

func TestImageRefusesMalformedBase64(t *testing.T) {
	d, _ := imageDeps(t)

	rec := do(d, http.MethodPost, "/api/sessions/abc123/image", `{"data":"not base64 at all!!"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestImageRefusesEmptyData(t *testing.T) {
	d, _ := imageDeps(t)

	rec := do(d, http.MethodPost, "/api/sessions/abc123/image", `{"data":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Same rule as the board directory: without somewhere of its own to write, this
// route does not guess. It says the panel is not wired for it.
//
// A relative directory counts as not wired, and that is the case worth pinning:
// it is resolved against the process's working directory — for a launch agent, a
// directory nobody chose — so a panel configured that way would scatter files
// wherever it was started from. Found while mutation-testing this file, when a
// disabled emptiness check turned "" into the relative path "abc123" and wrote
// three images into the repository itself.
func TestImageWithoutAnAbsoluteDirectoryIsUnavailable(t *testing.T) {
	for _, dir := range []string{"", "images", "./images", "../images"} {
		t.Run(dir, func(t *testing.T) {
			d, _ := testDeps()
			d.ImageDir = dir

			// Compared before and after rather than checked for absence: what
			// this asserts is that the request created nothing, and a directory
			// that was already there — left by an earlier run, or by anything
			// else — is not this request's doing and must not be read as it.
			before := workingDirEntries(t)
			rec := do(d, http.MethodPost, "/api/sessions/abc123/image", imageBody(pngBytes))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("want 503 for ImageDir %q, got %d: %s", dir, rec.Code, rec.Body.String())
			}
			for name := range workingDirEntries(t) {
				if !before[name] {
					t.Fatalf("a relative ImageDir wrote %q into the working directory", name)
				}
			}
		})
	}
}

// The image and its directory are the operator's screenshots, which routinely
// carry tokens and private work. They are not for other accounts on the machine.
func TestImageIsWrittenPrivately(t *testing.T) {
	d, _ := imageDeps(t)

	path := savedPath(t, do(d, http.MethodPost, "/api/sessions/abc123/image", imageBody(pngBytes)).Body.String())

	file, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := file.Mode().Perm(); perm != 0o600 {
		t.Fatalf("want file mode 0600, got %#o", perm)
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dir.Mode().Perm(); perm != 0o700 {
		t.Fatalf("want directory mode 0700, got %#o", perm)
	}
}

// This route writes a file, so it is a write, and it answers to the same two
// guards as every other write. Body type is checked here; the Origin rule is
// guard's own and covered in guard_test.go.
func TestImageRefusesANonJSONBody(t *testing.T) {
	d, _ := imageDeps(t)

	// text/plain is one of the three content types a browser sends cross-origin
	// with no preflight, which is exactly what the JSON requirement removes.
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/abc123/image",
		strings.NewReader(imageBody(pngBytes)))
	req.Header.Set("Content-Type", "text/plain")

	rec := send(d, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("want 415, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Only POST uploads. A method the router knows nothing about is refused by the
// router itself, and must not reach a handler that writes a file.
//
// GET is deliberately not the case tested here: the router ends with a catch-all
// "GET /" serving the page, so every unmatched GET — this path included — is that
// handler's 404 rather than a 405. That is the panel's shape, not this route's,
// and asserting 405 for GET would be asserting something the router cannot do.
func TestImageRouteIsPostOnly(t *testing.T) {
	d, dir := imageDeps(t)

	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			rec := do(d, method, "/api/sessions/abc123/image", imageBody(pngBytes))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("want 405, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("a refused method wrote %d entries", len(entries))
	}
}
