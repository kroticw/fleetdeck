# Workspace, empty board and first setup: field notes

These are facts from building the workspace a fleet keeps its board and documentation in, the empty board, and the first setup that makes both (#116, 2026-09-11). Each fact says how it was established: **measured** (observed on a stand), **read** (in code or a binary), or **decided** (a choice, with its reason). The code comments hold the details; this page holds what cost something to learn.

Read this before changing `internal/workspace`, `plugin/templates`, `internal/board/scan.go`, `internal/board/create.go`, the setup and permission steps of `cmd/fleetdeck/init.go`, or `setupDeps` in `cmd/fleetdeck/main.go`. Read section 1 before writing any wizard, command or page that makes something and records it.

The page is named after the code it is about, like [live-terminal.md](live-terminal.md), [window-and-panel.md](window-and-panel.md), [orchestrator-wizard.md](orchestrator-wizard.md) and [multiple-fleets.md](multiple-fleets.md). The setup page's own surface — the handler swap, the missing ETag, the `-port` stand — is in [window-and-panel.md](window-and-panel.md#first-launch-the-setup-surface).

## 1. A record written before the thing it records locks a failed setup in

This is the one defect on this page that no test found. A mutation found it, and it will come back in any flow that makes something and writes down that it did.

**The shape of the flow.** First setup is four steps, run by `initSteps` for `fleetdeck init` and for the setup page alike: the configuration file, the board (or workspace), Claude Code's statusline, Claude Code's permissions. The configuration names the board: `board.path`, and `docs.paths`.

**Decided, long before this change: `init` never rewrites a configuration it finds.** The file is hand-written YAML. Marshalling a struct back over it drops every comment and every ordering its author chose, so an existing file is read and kept byte for byte, and a flag that disagrees with it is refused: "`--workspace X` puts the board at X/board, which contradicts `board.path` Y already set in …". That refusal is right on its own. It protects the operator's configuration.

**Read: the first version wrote the configuration first.** Step one saved `config.yaml` naming the chosen board. Step two made the board. On success nothing is wrong: both exist, and they agree.

**The failure, step by step.** Take a board that cannot be made. A folder under a regular file does it; so does a disk without room, or a directory the user may not write.

1. The person chooses folder X on the setup page and presses Create.
2. Step one writes `config.yaml` with `board.path: X/board`.
3. Step two fails. The page reports it and stays, which is correct: there is no board to run on.
4. The person chooses folder Y and presses Create again.
5. Step one now finds a configuration. It keeps it, as it always does.
6. Step two is told Y, finds `board.path` X in the configuration, and refuses: Y contradicts X.

Nothing on the page can get out of this. The only way forward is deleting `~/.config/fleetdeck/config.yaml` by hand — on the first screen a new person ever sees, after the first thing they tried went wrong.

**What makes it a trap.** Neither rule is wrong alone. "Keep what you find" is right, and so is "record what you made". Together, with the record first, a failure in between turns the half-done record into something the next attempt treats as another person's decision. The half-done state becomes permanent.

**Why the tests did not see it.** Every test ran one attempt in a fresh home. The failing-board test started from no configuration and checked that the board step failed. The retry test started from a configuration it wrote itself, and checked that a contradicting flag was refused. Each property held. The sequence "fail, then correct" was in no test, because no test did two attempts in one home.

**How it was found.** It came out of planning a mutation, not out of a failing test. The setup's success condition was `ok := config step succeeded && board step succeeded`. A mutant keeping only the configuration half could only be killed by a test in which the board fails and the configuration does not. Writing that test meant tracing what the person does next — and the next step was the refusal. The mutant was not the defect. It pointed at a path no test had walked. After the fix, the same mutant survived its batch for the opposite reason (below).

**The fix, read in the code:**

- `ensureConfig` plans the new configuration and writes nothing. `saveNewConfig` writes it after the board step, and only when the board exists: "not written: the board it would name could not be made".
- With that order, a configuration exists only over a board, so the configuration step alone says whether the panel can run. The board half of the condition became unreachable and was deleted. Its mutant now has nothing to mutate.
- Two tests do two attempts in one home: `TestInitWritesNoConfigurationWhenTheBoardCannotBeMade`, and `TestASetupThatCannotMakeTheBoardCanBeCorrected` through the running panel.

**The rule, and where it already holds.** Make the thing first, check that it is there, then write the record that names it — and write the record last, since it is what the next attempt reads. The flows built after this one keep it. Read in the code:

- `fleetSteps` (`cmd/fleetdeck/init_fleet.go`, #120) makes the new fleet's board, and only then adds the fleet to the configuration. It checks everything the configuration would refuse before it makes anything.
- `Appoint` (`internal/orchestrator/appoint.go`, #118) writes the brief, delivers the message, and pins `orchestrator.session` last. A session that did not take the message is not pinned.

A flow that cannot put its record last has to recognise its own half-written record on the next attempt and replace it. Refusing it as somebody else's is the trap.

## 2. A workspace appears whole or not at all

**Decided:** the board is built in a staging directory beside its place (`.<name>.tmp-*` in the same parent) and renamed into place in one step. A copy that fails part way removes the staging directory and leaves nothing that looks like a board.

**Why.** A half-copied board — a README and no validator, or `cards/` and no `.gitignore` — passes for a board. The panel reads it, the agents write cards on it, and the missing piece shows up later in somebody else's task. No board at all is reported at once: the setup step fails and says why.

**Read: `os.Rename` on Unix refuses to replace an existing directory, even an empty one**, although `rename(2)` itself would. The first test of filling an empty board directory failed on exactly that. `placeBoard` removes an empty target first. `os.Remove` removes only an empty directory, so a file that arrived in between fails the call rather than being lost.

**Decided: a board directory that already holds anything is somebody's board.** It is kept as it is, whatever it holds, and so is an existing `docs`. An empty one is filled: that is a person who made the folder first.

**Decided: a missing git is reported, not fatal.** `git init` failing leaves the board without history, and the panel then writes card fields and says it could not commit them. That beats no board on a Mac where git is precisely what is missing (section 6). The new repository gets no commit: a signed commit would ask for a passphrase, and the panel has nobody to ask.

## 3. An empty board is a board: the sign is the directory, not a card

**Read: before this change an empty `cards/` was an error** (`ErrNoCards`, "no cards found"). The reason in the code was that an empty board was indistinguishable from a broken one, and `init` wrote an example card to avoid it. **Measured:** master's panel on a board with an empty `cards/` reported `no cards found in …/cards`.

**Decided:** the operator wanted a new board to start empty. The protection was not dropped. It was moved to a better sign. A board is a directory with a `cards` subdirectory. A `board.path` without one is still an error (`ErrNoCardsDir`), and that is exactly what a wrong path looks like. A `cards/` with no cards is an empty board with no error. **Measured:** the same empty board on this change: no error, zero cards.

The template keeps `cards/.gitkeep`, so an empty `cards` survives a clone of the board's repository and the board stays a board.

## 4. The board template is in the binary, and a test keeps it equal to the directory

**Decided:** `plugin/templates/board` — the README, the archive, the card validator and its tests — is embedded (`plugin/templates/templates.go`). A new board then gets the operator's current board with no checkout of this repository and no shell script. The bash script that used to make boards (`plugin/scripts/bootstrap-board.sh`) stays for machines without fleetdeck.

**Read:** `go:embed` skips names beginning with `.` or `_` when it walks a directory. `.gitignore` and `cards/.gitkeep` are therefore named in the directive one by one. `all:` is not used, for the reason `web/embed.go` gives: it would also take `.DS_Store` and editor files.

**Decided: a composition test, not only a behaviour test.** `TestBoardEmbedsEveryTemplateFile` walks the directory and the embedded tree and compares the lists. Without it, a file added to the template and forgotten in the directive would reach boards made by `upgrade-board.sh` and never the boards the panel makes, and nothing would say so. A second test names the six files, so that a file vanishing from both places at once is still caught. **Measured:** taking `.gitignore` out of the directive fails both tests.

**Read:** embedded files carry no mode. The scripts are made executable when they are written out, as the bash script did with `chmod +x`.

**Measured: "the same validator" is checked by running it.** A test runs `scripts/validate_cards.py` on a new board (it says there are no cards and exits zero), and on cards the panel starts in each of the four zones (valid).

## 5. Editing `~/.claude/settings.json` without touching what is not ours

Agents keep their cards on the board, outside their own working directory. Without the board in `permissions.additionalDirectories`, every card write is a permission prompt nobody is there to answer. So setup adds the workspace — or the board alone, when there is no workspace.

**Decided, the same rules as the statusline step before it:**

- a file that does not parse stops the step, and nothing is written over it;
- a `permissions` value that is not an object, or an `additionalDirectories` that is not a list, stops the step as well. It is somebody's own shape and is not replaced;
- a directory already listed, or inside a listed one, needs nothing: the file is not opened for writing at all, so a second run changes no byte;
- an entry may start with `~/`, and it is read against the home directory;
- "inside" is a path relation (`filepath.Rel`), not a string prefix: `/srv/fleet` does not hold `/srv/fleetdeck`. A mutation that compared prefixes was caught by a test of exactly that pair.

**Read: the file comes back re-encoded** — two-space indent, keys in alphabetical order. JSON carries no comments to lose, but a hand-ordered file does not come back in its order. The step says so when it happens, rather than leaving it to be discovered.

**Decided: the change to settings is announced before it happens.** The setup page says, before Create, which files outside the chosen folder it writes and why.

## 6. What the machine needs: git and python3 — documented, not measured

**Read:** the board's history is a git repository, and the validator agents run is a Python script. On a Mac without the Command Line Tools, `/usr/bin/git` and `/usr/bin/python3` are stubs that offer to install the tools.

**Documented, not measured:**

- The README and getting started say this, with `xcode-select --install` as the remedy.
- What was not done: no run on a Mac without the Command Line Tools.
- What is expected but not observed: running the git stub may itself pop the system's install dialog on the person's screen during setup.

The stand showed the other side. **Measured:** in a home with no git configuration, the board's commits carry an identity git makes up — the account's full name and `user@host.local` — and are unsigned. That is what a person who never configured git gets; the panel adds nothing of its own.

This dependency is the product's promise breaking on an assumption about the machine, not in this code. The validator was not rewritten in Go: the card asked for "the same validator".

## 7. Not verified

- **The system folder chooser was never opened.** It is a modal `NSOpenPanel` run from the web view's bound function (`cmd/fleetdeck-window/choose_darwin.go`), and it shows a window on the operator's screen, which needs their go-ahead. **Read:** webview_go calls a bound function synchronously, on the main thread. **Checked:** the binding's name on both sides (a mutation of either side fails a test), and that the page passes the chooser its words. It was not checked that `runModal` behaves inside WebKit's message handler.
- **The app window as a whole was not run through first setup.** The stand was the bare panel binary with headless Chrome. The app bundle carries no `fleetdeck-status`, so a first setup from the app refuses the statusline step and says so. The stand showed that refusal too, since its binary had no reporter beside it either.
- **A clean machine was a clean home.** "No board and no Obsidian" was an empty `HOME` on a machine that has git, python3 and Obsidian installed. The flow was shown to write nothing named "obsidian" and to need no vault, but not to run on a machine that never had them.

## 8. How the acceptance was measured

**The stand:**

- this tree's panel, on port 7792 with `-port`, and a `HOME` of its own;
- a daemon socket nobody listens on (`-stand-socket`);
- headless Chrome driven over the DevTools Protocol, with mouse events at element centres, `Input.insertText` for typing, and a key event for Enter.

**Measured on this change:**

- the setup page, then the folder typed as `~/work/fleet`;
- the board on screen 311 ms after Create, with six columns, zero cards and no board error;
- **+ card**, a title and a zone, Enter: the card in `new` 934 ms later, committed, and valid to the validator;
- `/` answered 200 twice, once for the setup page and once for the panel, and never 304.

**Measured on master, the same drive:** it stops at the first step. There is no setup page, `POST /api/cards` answers 405, and there is no **+ card** button. Master has no `-port`, so its stand had a configuration with only `server.port`, which makes it a configured panel without a board rather than a first launch.

**The control:** a build whose panel never swaps in after setup. The drive stops at the board. That is how a green run of the change is known to mean something.

**Mutation testing:**

- 94 compiling mutations, 93 killed.
- The survivor removes the error check on creating the workspace root. The next line's `ReadDir` catches the same case, so the check is a repeat.
- Four new tests kill no mutation. Three pin the shared unknown-field refusal and origin guard, which this change did not touch. The fourth pins the repeated check above.
