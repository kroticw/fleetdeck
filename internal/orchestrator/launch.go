package orchestrator

import (
	"cmp"
	"fmt"
)

// The defaults for a worker session: the strongest model, and a session that
// works unattended — auto mode rather than stopping at the first write, and no
// sandbox around its Bash commands. That is a real grant of trust, and
// docs/en/configuration.md says so where the workers section that changes it
// is described.
const (
	DefaultModel          = "opus"
	DefaultPermissionMode = "auto"
)

// Launch is how a worker session is started beyond where and under what name.
// The zero value is the defaults above with the sandbox off.
type Launch struct {
	Model          string
	PermissionMode string
	Sandbox        bool
}

// Args are the claude flags for l. Each is always given, a default included:
// a flag left out is a value the session takes from the operator's own
// settings file instead. The sandbox has no flag of its own — it is the
// settings key sandbox.enabled, so it goes in through --settings.
func (l Launch) Args() []string {
	return []string{
		"--model", cmp.Or(l.Model, DefaultModel),
		"--permission-mode", cmp.Or(l.PermissionMode, DefaultPermissionMode),
		"--settings", fmt.Sprintf(`{"sandbox":{"enabled":%t}}`, l.Sandbox),
	}
}
