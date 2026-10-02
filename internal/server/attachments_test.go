package server

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func attachmentBoard(t *testing.T) Deps {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"attachments/T-001/shot.png":  "png",
		"attachments/T-001/notes.txt": "<script>alert(1)</script>",
		"cards/T-001-x.md":            "---\nid: T-001\n---\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	d, _ := testDeps()
	d.BoardDir = dir
	return d
}

func getAttachment(d Deps, path string) *http.Response {
	return do(d, http.MethodGet, "/api/attachments?path="+url.QueryEscape(path), "").Result()
}

// A card's picture is shown in the card; the page asks for it by its place
// under the board's attachments.
func TestAnAttachedImageIsServedInline(t *testing.T) {
	res := getAttachment(attachmentBoard(t), "T-001/shot.png")
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("got %d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("an attachment must not be sniffed into something else")
	}
}

// Anything else is downloaded rather than opened: this origin can type into a
// live session, and a file dropped onto a card is not the panel's page.
func TestAnyOtherAttachmentIsADownloadThatRunsNothing(t *testing.T) {
	res := getAttachment(attachmentBoard(t), "T-001/notes.txt")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("got %d", res.StatusCode)
	}
	if !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("Content-Disposition = %q", res.Header.Get("Content-Disposition"))
	}
	if res.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("Content-Type = %q", res.Header.Get("Content-Type"))
	}
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("Content-Security-Policy = %q", res.Header.Get("Content-Security-Policy"))
	}
}

// Only the attachments directory is served: not a card, not a file beside the
// board.
func TestAttachmentsStayInsideTheirDirectory(t *testing.T) {
	d := attachmentBoard(t)
	for _, path := range []string{"../cards/T-001-x.md", "../../etc/passwd", "/etc/passwd", "T-001", ""} {
		if res := getAttachment(d, path); res.StatusCode == http.StatusOK {
			t.Fatalf("%q was served", path)
		}
	}
	if res := getAttachment(d, "T-001/missing.png"); res.StatusCode != http.StatusNotFound {
		t.Fatalf("a missing attachment: got %d", res.StatusCode)
	}
}
