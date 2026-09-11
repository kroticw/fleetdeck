package supervisor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// Step is one step of a handover from the window that runs an update to the
// window it starts from the newly built bundle.
type Step string

const (
	// StepAlive: the new window is up.
	StepAlive Step = "alive"
	// StepPanel: the new window's panel answers, started from the staged
	// bundle, with the new build.
	StepPanel Step = "panel"
	// StepSwapped: the new bundle is at the canonical path; the old one is
	// where the new one was built.
	StepSwapped Step = "swapped"
	// StepDone: the panel answers from the canonical path. The old window can
	// go.
	StepDone Step = "done"
	// StepFailed: the detail says why. Unless StepSwapped came first, the
	// canonical bundle is the one that was there before the update.
	StepFailed Step = "failed"
)

// Handover is how the two windows talk during an update: one file, one line
// per step -- "step<TAB>detail" -- appended by the new window and read by the
// old. A file rather than a pipe or a socket, because the new window is a
// separate app instance with nothing else shared with the old one but the
// disk, and because a file is still there to read when either side is gone.
type Handover struct{ Path string }

// Report appends one step.
func (h Handover) Report(s Step, detail string) error {
	f, err := os.OpenFile(h.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	// One write of one whole line: the reader acts only on complete lines.
	_, err = f.WriteString(string(s) + "\t" + strings.ReplaceAll(detail, "\n", " ") + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// handoverPoll is how often the old window looks at the handover file.
const handoverPoll = 50 * time.Millisecond

// Watch calls onStep for every step the new window reports, in order, and
// returns the last one when it is StepDone or StepFailed -- or ctx's error
// when ctx ends first.
func (h Handover) Watch(ctx context.Context, onStep func(Step, string)) (Step, string, error) {
	read := 0
	var last Step
	for {
		if data, err := os.ReadFile(h.Path); err == nil && len(data) > read {
			// Only up to the last newline: a line still being written is not
			// a step yet.
			end := strings.LastIndexByte(string(data), '\n') + 1
			for _, line := range strings.Split(strings.TrimSuffix(string(data[read:end]), "\n"), "\n") {
				if line == "" {
					continue
				}
				name, detail, _ := strings.Cut(line, "\t")
				last = Step(name)
				if onStep != nil {
					onStep(last, detail)
				}
				if last == StepDone || last == StepFailed {
					return last, detail, nil
				}
			}
			read = end
		}
		select {
		case <-ctx.Done():
			return last, "", fmt.Errorf("the new window reported %q and then nothing: %w", last, ctx.Err())
		case <-time.After(handoverPoll):
		}
	}
}
