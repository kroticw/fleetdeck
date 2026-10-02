package board

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

// maxAttachments bounds the files one card is started with. The bytes are
// bounded by the request that carries them.
const maxAttachments = 20

// Attachment is a file a card is started with: a pasted screenshot, a dropped
// document. Name is only a suggestion — it is cut to a safe file name.
type Attachment struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

// AttachmentsDir is where the attachments of the card numbered id live: beside
// the cards, so a card links them as ../attachments/<id>/<name>.
func AttachmentsDir(boardDir, id string) string {
	return filepath.Join(boardDir, "attachments", id)
}

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

// IsImage says whether a file is shown as a picture rather than offered as a
// download: raster formats only, an SVG can carry script.
func IsImage(name string) bool {
	return imageExts[strings.ToLower(path.Ext(name))]
}

func checkAttachments(atts []Attachment) error {
	if len(atts) > maxAttachments {
		return fmt.Errorf("%w: more than %d attachments", ErrInvalidCard, maxAttachments)
	}
	for _, a := range atts {
		if len(a.Data) == 0 {
			return fmt.Errorf("%w: attachment %q is empty", ErrInvalidCard, a.Name)
		}
	}
	return nil
}

// attachmentName cuts a suggested name to its last element, with letters,
// digits, dot, dash and underscore kept and every other run turned into one
// dash: a name that goes into a markdown link must hold no space or bracket.
func attachmentName(suggested string) string {
	base := suggested[strings.LastIndexAny(suggested, `/\`)+1:]
	var b strings.Builder
	dash := false
	for _, r := range base {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
			continue
		}
		dash = true
	}
	name := strings.TrimLeft(b.String(), ".")
	if name == "" {
		return "file"
	}
	return name
}

// writeAttachments puts atts into dir under names made unique by a -2, -3
// suffix, and returns the card's section listing them.
func writeAttachments(dir, id string, atts []Attachment) (string, error) {
	if len(atts) == 0 {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create attachments: %w", err)
	}
	var section strings.Builder
	section.WriteString("\n## Вложения\n\n")
	used := make(map[string]bool, len(atts))
	for _, a := range atts {
		name := attachmentName(a.Name)
		ext := path.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s-%d%s", stem, n, ext)
		}
		used[name] = true
		if err := os.WriteFile(filepath.Join(dir, name), a.Data, 0o600); err != nil {
			return "", fmt.Errorf("write attachment %s: %w", name, err)
		}
		bang := ""
		if IsImage(name) {
			bang = "!"
		}
		fmt.Fprintf(&section, "- %s[%s](../attachments/%s/%s)\n", bang, name, id, name)
	}
	return section.String(), nil
}
