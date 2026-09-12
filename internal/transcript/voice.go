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
// anything, and the tool call it is standing inside, if it is inside one.
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
	// LastSpoke is when the session last said anything. A session that has said
	// nothing at all yet counts from the first timestamped line of its transcript:
	// it has said nothing since then. Zero only when there is no timestamped line to
	// measure from, which the caller reads as "not measured".
	LastSpoke time.Time
	// InCall is the tool call the session is standing inside, or nil. It is the fact
	// the transcript records, not a judgement: a call that is slow and a call that
	// will never return look the same from here.
	InCall *Call
}

// Call is one open tool call: the tool's name and when the call was made.
type Call struct {
	Tool  string    `json:"tool"`
	Since time.Time `json:"since"`

	id string
}

// maxSubagentDepth bounds how far ReadVoice follows a subagent into the subagent it
// started in turn. Claude Code records nesting (spawnDepth in the meta file) but a
// chain of meta files naming each other is still a cycle a reader must not follow
// forever.
const maxSubagentDepth = 4

// ReadVoice reads the session's voice from the tail of its transcript. Only the most
// recent turn is read in the common case; the walk goes further back only while
// nothing the session said has been found yet.
func ReadVoice(path string) (Voice, error) {
	subagents := filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents")
	return readVoice(path, subagents, 0)
}

func readVoice(path, subagentsDir string, depth int) (Voice, error) {
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
		sawCall   bool
		spokeSeen bool
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
				v.LastSpoke, spokeSeen = stamp, true
			}
		case r.Type == "user":
			// A prompt, from a person or a peer: someone speaking to the session.
			// It ends the turn being read, and says nothing about the session.
			return spokeSeen
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
					v.InCall = &Call{Tool: u.Name, Since: stamp, id: u.ID}
				}
			}
		}
		return false
	}
	if err := reverseLines(f, visit); err != nil {
		return Voice{}, err
	}
	if !spokeSeen {
		v.LastSpoke = earliest
	}

	if v.InCall != nil && depth < maxSubagentDepth {
		if sub, ok := subagentOf(subagentsDir, v.InCall.id); ok {
			if sv, err := readVoice(sub, subagentsDir, depth+1); err == nil && sv.LastSpoke.After(v.LastSpoke) {
				v.LastSpoke = sv.LastSpoke
			}
		}
	}
	return v, nil
}

// subagentOf finds the transcript of the subagent a tool call started, through the
// meta file Claude Code writes beside it ("agent-<id>.meta.json", whose toolUseId
// names the call). A call with no such file started no subagent this reader can see.
func subagentOf(dir, toolUseID string) (string, bool) {
	if toolUseID == "" {
		return "", false
	}
	metas, err := filepath.Glob(filepath.Join(dir, "*.meta.json"))
	if err != nil {
		return "", false
	}
	for _, m := range metas {
		raw, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var meta struct {
			ToolUseID string `json:"toolUseId"`
		}
		if json.Unmarshal(raw, &meta) != nil || meta.ToolUseID != toolUseID {
			continue
		}
		return strings.TrimSuffix(m, ".meta.json") + ".jsonl", true
	}
	return "", false
}

// voiceLine is the part of a transcript line ReadVoice reads.
type voiceLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
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
