# Limitations

What fleetdeck does not do, and what it touches on the machine it runs on.

## macOS only

Two things shell out to macOS tools with no fallback: reading the Anthropic OAuth token from the Keychain, through the `security` command, and showing notification banners, through `osascript`. Neither has a cross-platform equivalent in this codebase.

## The local daemon only

fleetdeck talks to the Claude Code daemon over its Unix control socket on the same machine. There is no remote or networked mode.

## The board needs git and python3

The board's history is a git repository, and the card validator agents run is a Python script. On a Mac without the Command Line Tools, `/usr/bin/git` and `/usr/bin/python3` are stubs that offer to install them.

The board is made without either. The panel then cannot commit its card writes, and agents cannot validate their cards. `xcode-select --install` provides both.

## What it reads under your home directory

Three things, and nothing else:

- the Keychain item holding the Claude Code OAuth credentials;
- markdown files under the configured board path, and the git repository containing them;
- the session transcript files Claude Code writes.

## Where credentials go

The OAuth token read from the Keychain is sent to exactly one host, `api.anthropic.com`. It is never logged, never written to disk, and never passed to a browser.

## A banner is not confirmed delivery

`osascript`'s exit code is the only signal fleetdeck has, and a zero exit means the command ran — not that a banner appeared. macOS drops notifications silently when permission is denied or a Focus mode is on, and `osascript` still exits zero.

Nothing here checks on-screen delivery, because that would mean reading an undocumented private database. A banner that never appeared is therefore not an error fleetdeck can report. The panel's own counters do not depend on any of this, and are what to trust for whether something is waiting.

## Inline styles, for one library

The panel's Content-Security-Policy is `'self'` everywhere except `style-src`, which also carries `'unsafe-inline'`.

The vendored xterm.js builds `<style>` elements at runtime and fills them with the terminal's measured cell size and theme colours, and that version has no nonce option. Without the keyword the terminal draws in a proportional font with no colour.

`script-src` and `default-src` stay `'self'`, so what this allows is an injected appearance, not injected behaviour. See the comment on `contentSecurityPolicy` in `internal/server/static.go`.
