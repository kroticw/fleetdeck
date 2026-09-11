// Package docs embeds the one piece of this documentation the binary itself
// hands out: the orchestrator's working order, in both languages. The first-run
// wizard gives it to the session it appoints (internal/orchestrator).
//
// The directive lives here for the reason web/embed.go gives: a go:embed
// pattern cannot climb out of its own package directory, and the file is
// documentation first — it is read, reviewed and edited here, beside its
// translation, not copied into a Go package where the two would drift apart.
package docs

import "embed"

// Orchestrator holds en/orchestrator.md and ru/orchestrator.md.
//
//go:embed en/orchestrator.md ru/orchestrator.md
var Orchestrator embed.FS
