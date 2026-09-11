package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// This route is the panel's third kind of write, and the only one that accepts
// bytes rather than a short string. Spec section 5 says the panel performs
// exactly two writes — text into a session, and one field of one card — so this
// is a deliberate addition to that list, made because the operator asked to
// attach images to a session the way the terminal interface does, and it is
// worth stating what it does and does not do.
//
// What it does: takes an image, writes it under a directory of the panel's own,
// and hands back the path. Nothing else. The image reaches the session through
// the write that already exists — the text route, carrying that path — and the
// agent reads the file with its own Read tool, which returns an image. Verified
// against a live session: the marker the terminal interface uses ("[Image #N]")
// is resolved inside that process from a paste buffer nothing outside it can
// reach, so it cannot be reproduced from here, while a plain path can.
//
// What it deliberately does not do: send anything. Which text accompanies an
// image, and whether it is sent at all, stays with the operator — two images and
// one question is an ordinary thing to want, and a route that sent on upload
// could not express it.
//
// One consequence the operator sees, and it is not hidden here: the file lands
// outside the session's working directory, so the agent's first read of it asks
// for permission. That is one answer per session (the prompt offers to allow the
// whole directory), it is answerable from the panel, and it shows up in the
// "waiting for you" counter like any other question. The alternative — writing
// into the operator's own repository — leaves files in a working tree that
// eventually reach somebody's commit, which is not undone by answering a prompt.

const (
	// maxImageBytes bounds one image, measured after decoding, because the
	// decoded size is what reaches the disk. A screenshot of a 5K display in PNG
	// sits comfortably under this; the terminal interface itself re-encodes
	// anything large to JPEG at around 500 KB before sending it to the model, so
	// this ceiling is generous rather than tight.
	maxImageBytes = 8 << 20

	// maxImageBodyBytes bounds the request itself, while it is being read, so a
	// caller cannot make the panel buffer an unbounded stream.
	//
	// It is not simply base64's overhead on top of maxImageBytes, and the
	// difference is the whole point. Base64 costs a third, so an 8 MiB image
	// arrives as about 11.2 MiB; a body ceiling set just above that leaves a
	// window of a few dozen kilobytes in which an oversized *image* is refused
	// for being an oversized image. Every image past it would be cut off by the
	// body reader instead, whose message talks about request bytes and sends the
	// operator looking for the wrong limit. Measured against the running binary:
	// a 9 MiB PNG was refused with "request body must not exceed 11250346 bytes"
	// rather than a word about the 8 MiB image ceiling.
	//
	// 16 MiB leaves room for an image of roughly 12 MiB to be read in full and
	// then refused by maxImageBytes, with a message naming the limit that
	// actually applies — a comfortable margin over the 8 MiB image ceiling rather
	// than a few kilobytes of one. Past that the read ceiling takes over, which
	// is what it is for.
	maxImageBodyBytes = 16 << 20

	// imageDirPerm and imageFilePerm keep an operator's screenshots to the
	// operator. They routinely carry tokens, private repositories and customer
	// data, and this is a shared machine as far as the filesystem is concerned.
	imageDirPerm  = 0o700
	imageFilePerm = 0o600
)

// imageTypes maps what content sniffing recognises onto the extension the file
// gets. It is an allowlist: a type absent from it is not written at all.
//
// The extension follows the bytes rather than any claim in the request, because
// the file's name is what every later reader — the agent's Read tool, the
// operator's own eyes — uses to decide what it is holding.
var imageTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// sessionIDSafe reports whether an id can be used as a single path element.
//
// This is the one place this route builds a path out of something the caller
// sent, so it is where the rule from spec section 8 applies. The check is an
// allowlist of characters, not a search for "..": a dot is not in the allowlist
// at all, which makes "..", ".hidden" and every variation of them impossible
// rather than merely handled. A separator is likewise absent, so the id can only
// ever name one directory, never a path.
//
// Percent-encoded separators need no special case: net/http decodes the path
// before the pattern matches, so "%2f" arrives here as "/" and fails the same
// check that a literal one does.
func sessionIDSafe(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

func (d Deps) handleUploadImage(w http.ResponseWriter, r *http.Request) {
	// Absolute, not merely non-empty. A relative directory is resolved against
	// the process's working directory — for a panel started by the window, the
	// directory an app from the Dock is given, which nobody chose — so a panel configured that way scatters files wherever it was
	// started from. Observed while mutation-testing this file: with the check
	// disabled, an empty ImageDir became the relative path "abc123" and wrote
	// three images straight into the repository. A configured-but-relative
	// directory does the same thing with no mutation at all, so this checks for
	// what is required rather than for the one value that is obviously wrong.
	if !filepath.IsAbs(d.ImageDir) {
		unavailable(w, "an absolute directory to keep session images in")
		return
	}

	id := r.PathValue("id")
	if !sessionIDSafe(id) {
		fail(w, http.StatusBadRequest, "session id must be letters, digits, dashes or underscores")
		return
	}

	var body struct {
		Data string `json:"data"`
	}
	// decodeBody refuses an unknown field, which is what keeps a "name" or
	// "filename" key from ever being read. The file's name is the server's.
	if !decodeBodyLimit(w, r, &body, maxImageBodyBytes) {
		return
	}
	if body.Data == "" {
		fail(w, http.StatusBadRequest, "data is required: an upload must carry an image")
		return
	}

	raw, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		fail(w, http.StatusBadRequest, "data must be base64: "+err.Error())
		return
	}
	if len(raw) > maxImageBytes {
		fail(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("an image must not exceed %d bytes, got %d", maxImageBytes, len(raw)))
		return
	}

	// http.DetectContentType implements the WHATWG sniffing algorithm over the
	// first 512 bytes. What the request claimed is not consulted, because a
	// claim is exactly what an attacker controls.
	ext, ok := imageTypes[mediaType(http.DetectContentType(raw))]
	if !ok {
		fail(w, http.StatusUnsupportedMediaType, "only PNG, JPEG, GIF and WebP images are accepted")
		return
	}

	dir := filepath.Join(d.ImageDir, id)
	if err := os.MkdirAll(dir, imageDirPerm); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	name, err := imageName(ext)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, imageFilePerm); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

// mediaType drops the parameters DetectContentType appends to some types
// ("text/plain; charset=utf-8"). None of the image types carry one today; taking
// only the type keeps the lookup correct if that ever changes.
func mediaType(detected string) string {
	if cut := strings.IndexByte(detected, ';'); cut >= 0 {
		return strings.TrimSpace(detected[:cut])
	}
	return detected
}

// imageName invents the file's name. It is random rather than sequential
// because a counter would have to be kept somewhere, and two uploads racing for
// the same number would have one silently replace the other's image.
func imageName(ext string) (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("could not name the file: %w", err)
	}
	return hex.EncodeToString(raw[:]) + ext, nil
}
