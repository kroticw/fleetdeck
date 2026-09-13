package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Voice is what a transcript says about the session's own voice: when it last said
// anything, since when it has left something unanswered, and the tool call it is
// standing inside when that call is on disk.
//
// "Said" is narrower than "written". A transcript is appended to by more than the
// session: a message sent to it lands as a queue-operation line the moment it is sent,
// runtime reminders land as attachments, and a rename or a mode change adds a
// bookkeeping line with no timestamp at all. None of that is the session speaking, and
// all of it moves the file's modification time. Measured on a live session frozen
// inside a tool call on 2026-09-12: a message sent to it took the file's age from 82
// seconds to 5, while the last line the session wrote itself did not move. Silence
// measured by modification time is therefore reset by exactly the person trying to
// reach a session that cannot answer.
//
// So only two kinds of line count as the session speaking: an assistant line (the
// model said or started something) and a user line carrying a tool_result (a call the
// session made came back to it). A census of every transcript on this machine over
// the seven days to 2026-09-12 found no other line kind the session writes on its own
// behalf.
type Voice struct {
	// LastSpoke is when the session, or a subagent of it, last said anything. A
	// session that has said nothing at all yet counts from the first timestamped line
	// of its transcript: it has said nothing since then. Zero only when there is no
	// timestamped line to measure from, which the caller reads as "not measured".
	LastSpoke time.Time

	// Unanswered is the moment since which the session has owed a move and said
	// nothing, or zero when it owes nothing -- when its own words are the newest thing
	// in the transcript.
	//
	// It owes a move when the newest of its lines is a call that came back, or a call
	// still open, or when someone addressed it -- a typed prompt, a queued message --
	// after its last words. The first two count from the session's last word; the
	// third from the first address after it, not from the end of its turn, which may
	// be hours ago. A subagent speaking restarts the count, since the session is then
	// working through it.
	//
	// This, and not InCall, is what outlives a frozen call. Claude Code does not put
	// every open call on disk: measured on CLI 2.1.269, a Read -- alone, or beside a
	// Bash -- reached the transcript only together with its result, so for the whole
	// time the call hung the file ended at the previous result. What the file does show
	// in that state is a session that owes its next move and has not made it.
	Unanswered time.Time

	// InCall is the tool call the session is standing inside, or nil. It is a fact the
	// transcript records only for calls Claude Code writes before they return -- a
	// Bash, an MCP call -- and never a judgement: a call that is slow and a call that
	// will never return look the same from here.
	InCall *Call
}

// Call is one open tool call: the tool's name and when the call was made.
type Call struct {
	Tool  string    `json:"tool"`
	Since time.Time `json:"since"`
}

// ReadVoice reads the session's voice from the tail of its transcript, and from the
// transcripts of its subagents beside it. Only the most recent turn is read in the
// common case; the walk goes further back only while nothing the session said has
// been found yet.
func ReadVoice(path string) (Voice, error) {
	v, err := readOwnVoice(path)
	if err != nil {
		return Voice{}, err
	}
	own := v.LastSpoke
	if sub := subagentsLastSpoke(filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents"), own); sub.After(own) {
		v.LastSpoke = sub
		if !v.Unanswered.IsZero() && sub.After(v.Unanswered) {
			v.Unanswered = sub
		}
	}
	return v, nil
}

func readOwnVoice(path string) (Voice, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Voice{}, fmt.Errorf("%w: %s", ErrNoTranscript, path)
	}
	if err != nil {
		return Voice{}, fmt.Errorf("open transcript: %w", err)
	}
	defer func() { _ = f.Close() }()

	var (
		v         Voice
		earliest  time.Time
		answered  = map[string]bool{}
		addresses []time.Time // addresses seen before (newer than) the last own line
		sawCall   bool
		spokeSeen bool
		lastIsRes bool // the newest own line is a tool_result
	)
	visit := func(line []byte) bool {
		var r voiceLine
		if json.Unmarshal(line, &r) != nil {
			return false
		}
		stamp, err := time.Parse(time.RFC3339, r.Timestamp)
		if err != nil {
			return false
		}
		earliest = stamp

		blocks := r.blocks()
		switch {
		case r.addresses(blocks):
			if !spokeSeen {
				addresses = append(addresses, stamp)
			}
			// A typed prompt before the session's newest words starts the turn
			// those words belong to: nothing further back is needed.
			return spokeSeen && r.Type == "user"
		case r.Type == "user" && blocks.hasToolResult():
			if sawCall {
				// A result from before the calls already seen: the previous
				// batch. Everything the current turn holds has been read.
				return true
			}
			for _, id := range blocks.toolResultIDs() {
				answered[id] = true
			}
			if !spokeSeen {
				v.LastSpoke, spokeSeen, lastIsRes = stamp, true, true
			}
		case r.Type == "assistant":
			if !spokeSeen {
				v.LastSpoke, spokeSeen = stamp, true
			}
			uses := blocks.toolUses()
			if len(uses) == 0 {
				// Thinking or text: the model speaking before its calls, or
				// after all of them came back. Either way the turn starts here.
				return true
			}
			sawCall = true
			for _, u := range uses {
				if !answered[u.ID] {
					// Walking newest-first, so the last one kept is the
					// earliest open call of the batch.
					v.InCall = &Call{Tool: u.Name, Since: stamp}
				}
			}
		}
		return false
	}
	if err := reverseLines(f, visit); err != nil {
		return Voice{}, err
	}

	switch {
	case !spokeSeen:
		v.LastSpoke = earliest
		v.Unanswered = earliestAtOrAfter(addresses, earliest)
	case lastIsRes || v.InCall != nil:
		v.Unanswered = v.LastSpoke
	default:
		v.Unanswered = earliestAfter(addresses, v.LastSpoke)
	}
	return v, nil
}

// subagentsLastSpoke is the newest moment any subagent of the session spoke after
// since, or the zero time. Only transcripts written to after since are read at all,
// so an idle session with a long history of finished subagents costs a directory
// listing and nothing more.
func subagentsLastSpoke(dir string, since time.Time) time.Time {
	paths, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return time.Time{}
	}
	var newest time.Time
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil || !fi.ModTime().After(since) {
			continue
		}
		sv, err := readOwnVoice(p)
		if err != nil {
			continue
		}
		if sv.LastSpoke.After(newest) {
			newest = sv.LastSpoke
		}
	}
	return newest
}

func earliestAfter(ts []time.Time, after time.Time) time.Time {
	var out time.Time
	for _, t := range ts {
		if t.After(after) && (out.IsZero() || t.Before(out)) {
			out = t
		}
	}
	return out
}

func earliestAtOrAfter(ts []time.Time, from time.Time) time.Time {
	var out time.Time
	for _, t := range ts {
		if !t.Before(from) && (out.IsZero() || t.Before(out)) {
			out = t
		}
	}
	return out
}

// voiceLine is the part of a transcript line ReadVoice reads.
type voiceLine struct {
	Type       string `json:"type"`
	Timestamp  string `json:"timestamp"`
	Operation  string `json:"operation"`
	Attachment struct {
		Type string `json:"type"`
	} `json:"attachment"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// addresses reports whether the line is someone addressing the session: a typed
// prompt, a message put in its queue, or the record of a queued message being handed
// to it.
func (r voiceLine) addresses(blocks blockList) bool {
	switch r.Type {
	case "user":
		return !blocks.hasToolResult()
	case "queue-operation":
		return r.Operation == "enqueue"
	case "attachment":
		return r.Attachment.Type == "queued_command"
	}
	return false
}

type contentBlock struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
}

type blockList []contentBlock

// blocks decodes the message content as a list of blocks. A plain-string content (how
// a typed prompt is written) has no blocks, which is what every caller wants from it.
func (r voiceLine) blocks() blockList {
	var bl blockList
	if json.Unmarshal(r.Message.Content, &bl) != nil {
		return nil
	}
	return bl
}

func (bl blockList) hasToolResult() bool {
	for _, b := range bl {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

func (bl blockList) toolResultIDs() []string {
	var ids []string
	for _, b := range bl {
		if b.Type == "tool_result" {
			ids = append(ids, b.ToolUseID)
		}
	}
	return ids
}

func (bl blockList) toolUses() []contentBlock {
	var uses []contentBlock
	for _, b := range bl {
		if b.Type == "tool_use" {
			uses = append(uses, b)
		}
	}
	return uses
}
