package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/state"
)

const (
	// maxBodyBytes bounds every request body. A prompt typed into a session is the
	// largest thing that legitimately arrives here, and 1 MiB is far more than a
	// human types in one go while still being small enough that a runaway client
	// cannot make the panel buffer anything interesting.
	maxBodyBytes = 1 << 20
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	// The status line and headers are already on the wire by now, so a failed
	// encode has nowhere to be reported to the client. Logging is the caller's
	// concern; this package writes to no log of its own.
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// unavailable answers a route whose dependency is nil. A panel wired without a
// capability says so with 503; it must never panic and must never pretend the
// route does not exist.
func unavailable(w http.ResponseWriter, what string) {
	fail(w, http.StatusServiceUnavailable, "this panel is not wired to "+what)
}

// decodeBody reads exactly one JSON value from the request body into v, and
// writes the failure itself when there is one — a handler only has to stop.
//
// Three rules, all deliberate: the body is bounded (an oversized one is 413),
// unknown fields are refused rather than dropped, so a typo fails loudly, and a
// second JSON value after the first is refused rather than ignored.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeBodyLimit(w, r, v, maxBodyBytes)
}

// decodeBodyLimit is decodeBody with the ceiling named by the caller. It exists
// because maxBodyBytes is sized for a typed prompt, and one route carries an
// image instead — see image.go. The limit is a parameter rather than a second
// copy of this function so the three rules above cannot drift apart between
// them, and it is applied here rather than by the caller because
// http.MaxBytesReader replaces the body: a wrapper applied outside would be
// silently overwritten by the one this function sets.
func decodeBodyLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return decodeFailed(w, err)
	}
	var trailing json.RawMessage
	switch err := dec.Decode(&trailing); {
	case errors.Is(err, io.EOF):
		return true
	case err != nil:
		return decodeFailed(w, err)
	default:
		fail(w, http.StatusBadRequest, "body must contain exactly one JSON object")
		return false
	}
}

func decodeFailed(w http.ResponseWriter, err error) bool {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		// The limit comes from the error, not from maxBodyBytes: routes no longer
		// share one ceiling, and a message naming a limit other than the one that
		// actually refused the request sends the reader to the wrong constant.
		fail(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("request body must not exceed %d bytes", tooLarge.Limit))
		return false
	}
	fail(w, http.StatusBadRequest, "malformed body: "+err.Error())
	return false
}

func (d Deps) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if d.Snapshot == nil {
		unavailable(w, "a snapshot source")
		return
	}
	view, err := d.fleetView(r)
	if err != nil {
		fail(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// fleetView is the snapshot of the fleet the request names in its fleet
// query parameter, the first fleet when it names none. The fleet lives in the
// tab's address, not in the server: two tabs on two fleets are served side by
// side, and switching one of them changes nothing for the other. The only
// error is a fleet no configuration has, which the caller answers with 404.
func (d Deps) fleetView(r *http.Request) (state.Snapshot, error) {
	return state.ForFleet(d.Snapshot(), r.URL.Query().Get(fleetParam))
}

func (d Deps) handleSendText(w http.ResponseWriter, r *http.Request) {
	if d.SendText == nil {
		unavailable(w, "a daemon")
		return
	}
	var body struct {
		Text string `json:"text"`
		// Submit is a pointer so an absent field can be told apart from an
		// explicit false. The daemon's reply operation always delivers and
		// submits — there is no way to place text in a session's prompt without
		// sending it — so an explicit false is refused rather than accepted and
		// ignored, which would be a promise this server cannot keep.
		Submit *bool `json:"submit"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Submit != nil && !*body.Submit {
		fail(w, http.StatusBadRequest, "submit:false is not possible: the daemon always submits, and text cannot be placed in a session's prompt without sending it")
		return
	}
	if err := d.SendText(r.PathValue("id"), body.Text); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handlePatchCard(w http.ResponseWriter, r *http.Request) {
	d, ok := d.forFleet(w, r)
	if !ok {
		return
	}
	if d.SetCardField == nil {
		unavailable(w, "a board")
		return
	}
	if d.BoardDir == "" {
		// A panel that was never told where its board is cannot confine a write,
		// and must not guess: the path in the request would then be free to name
		// any file on the disk that has frontmatter.
		unavailable(w, "a board directory")
		return
	}
	var body struct {
		Path  string `json:"path"`
		Field string `json:"field"`
		Value string `json:"value"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Path == "" {
		fail(w, http.StatusBadRequest, "path is required: a card write must name the card it writes")
		return
	}
	path, err := confineToBoard(d.BoardDir, body.Path)
	switch {
	case errors.Is(err, errOutsideBoard):
		fail(w, http.StatusForbidden, "card writes are confined to the board directory")
		return
	case err != nil:
		// The board directory itself could not be resolved, so nothing can be
		// checked against it. That is the panel's configuration being wrong, not
		// the request.
		unavailable(w, "a readable board directory")
		return
	}
	switch err := d.SetCardField(path, body.Field, body.Value); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, board.ErrNothingToCommit):
		// The card already held this value, so the write changed no bytes and
		// there was no history to record. Nothing happened and nothing is
		// missing: an ordinary success, checked before the case below because an
		// error can carry both sentinels and this is the more specific one.
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrFieldWrittenNotCommitted):
		// A success status for a partial outcome, deliberately. The status code
		// answers one question — did the operator's edit take effect? — and here
		// it did: the field is in the card file. What failed is the commit, and
		// the panel cannot ask for that to be retried by redoing the edit, which
		// on a progress field would apply it twice. So the outcome goes out as a
		// success carrying the part that did not happen and why, for the panel to
		// show as it likes.
		writeJSON(w, http.StatusOK, map[string]any{
			"written":   true,
			"committed": false,
			"reason":    err.Error(),
		})
	default:
		fail(w, cardWriteStatus(err), err.Error())
	}
}

// handleCreateCard starts a card from a title and a zone. The body carries those
// two fields and nothing else: a card is started here and written by whoever
// takes the task on, so anything more is refused rather than half obeyed.
func (d Deps) handleCreateCard(w http.ResponseWriter, r *http.Request) {
	d, ok := d.forFleet(w, r)
	if !ok {
		return
	}
	if d.CreateCard == nil {
		unavailable(w, "a board")
		return
	}
	var body struct {
		Title string `json:"title"`
		Zone  string `json:"zone"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	path, err := d.CreateCard(body.Title, body.Zone)
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, map[string]any{"path": path, "committed": true})
	case errors.Is(err, ErrCardWrittenNotCommitted):
		// Created, deliberately: the card is on the board, and a failure here
		// would invite the operator to make a second one.
		writeJSON(w, http.StatusCreated, map[string]any{"path": path, "committed": false, "reason": err.Error()})
	case errors.Is(err, board.ErrInvalidCard):
		fail(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, board.ErrNoCardsDir):
		fail(w, http.StatusServiceUnavailable, err.Error())
	default:
		fail(w, http.StatusInternalServerError, err.Error())
	}
}

// cardWriteStatus maps what internal/board can actually return onto a status. It
// sees only the failures where nothing was written: the two outcomes where the
// field did reach the card are answered by the caller above.
//
// board.SetField distinguishes exactly one failure with a sentinel
// (ErrUnknownField, for a field the panel does not own) and reaches the disk only
// after its own validation has passed. So: the sentinel is the caller asking for
// something the panel will never do (400); a wrapped fs.ErrNotExist is a card that
// is not there (404); any other filesystem error is the write itself failing
// (500); and everything left is board refusing the value — an unknown stage, a
// progress step off the ladder, a cross-field rule — which is the caller's mistake
// (400).
//
// One family of refusals is not about the request at all: a card whose own
// content cannot accept the write — no frontmatter block, frontmatter that will
// not parse, no line for the field, a substitution that would change the file's
// line count. The request was well formed and the card is not, so it answers 422
// rather than telling the operator they asked wrongly.
//
// The last branch is a default rather than a test, because board returns those
// refusals as plain errors with no sentinel to match on. Filesystem failures are
// recognised structurally instead of by message, which is why the default can be
// the caller's mistake rather than ours.
func cardWriteStatus(err error) int {
	switch {
	case errors.Is(err, board.ErrUnknownField):
		return http.StatusBadRequest
	case errors.Is(err, fs.ErrNotExist):
		return http.StatusNotFound
	case isFilesystemError(err):
		return http.StatusInternalServerError
	case isUnwritableCard(err):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusBadRequest
	}
}

// unwritableCard lists board.SetField's own wording for the failures that are
// the card's fault rather than the request's.
//
// Matching on message text is a seam, and it is here because internal/board
// draws no sentinel around this family: a reworded message there turns a 422
// silently back into a 400, which no test in this package would notice. A
// sentinel in internal/board would close it properly.
var unwritableCard = []string{
	"has no frontmatter to write into",
	"has malformed frontmatter",
	"line count would change",
}

// hasNoFieldLine matches board's "card <path> has no <field> field", a card whose
// frontmatter simply does not carry the line the write would replace.
var hasNoFieldLine = regexp.MustCompile(`has no [a-z]+ field`)

func isUnwritableCard(err error) bool {
	msg := err.Error()
	for _, phrase := range unwritableCard {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return hasNoFieldLine.MatchString(msg)
}

func isFilesystemError(err error) bool {
	var pathErr *fs.PathError
	var linkErr *os.LinkError
	return errors.As(err, &pathErr) || errors.As(err, &linkErr)
}

// handleStatus receives what cmd/fleetdeck-status posts. The four fields below are
// that reporter's wire format, field for field: the model display name, cost and
// context percentage are the three values Claude Code hands its statusline command
// and that nothing outside the session can obtain any other way (spec section 3.2).
func (d Deps) handleStatus(w http.ResponseWriter, r *http.Request) {
	if d.PutStatus == nil {
		unavailable(w, "a store for statusline reports")
		return
	}
	var body struct {
		SessionID      string  `json:"sessionId"`
		Model          string  `json:"model"`
		CostUSD        float64 `json:"costUSD"`
		ContextPercent float64 `json:"contextPercent"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.SessionID == "" {
		// A report with no session cannot be attached to anything. Storing it
		// under an empty key would make every unattributable report overwrite the
		// last one, which is worse than losing it loudly.
		fail(w, http.StatusBadRequest, "sessionId is required: a report that names no session cannot be attributed")
		return
	}
	d.PutStatus(body.SessionID, body.Model, body.ContextPercent, body.CostUSD)
	w.WriteHeader(http.StatusNoContent)
}

// handleDigest serves the readable moments of a session's transcript. id is
// the transcript UUID (daemon.Session.SessionID), not the daemon's short id —
// transcript.Locate matches on the UUID and nothing else.
func (d Deps) handleDigest(w http.ResponseWriter, r *http.Request) {
	if d.Digest == nil {
		unavailable(w, "a transcript reader")
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	steps, err := d.Digest(r.PathValue("id"), limit)
	if err != nil {
		fail(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, steps)
}

// handlePatchConfig writes the one setting spec section 11 has this panel
// write: which session is pinned to the orchestrator column. The field is a
// pointer because an absent key (400: nothing was asked for) and an explicit
// empty string (204: unpin, a legal request) are different outcomes — the
// same device handleSendText uses for Submit *bool.
func (d Deps) handlePatchConfig(w http.ResponseWriter, r *http.Request) {
	d, ok := d.forFleet(w, r)
	if !ok {
		return
	}
	if d.SetOrchestratorSession == nil {
		unavailable(w, "a configuration store")
		return
	}
	var body struct {
		OrchestratorSession *string `json:"orchestratorSession"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.OrchestratorSession == nil {
		fail(w, http.StatusBadRequest, "orchestratorSession is required")
		return
	}
	if err := d.SetOrchestratorSession(*body.OrchestratorSession); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrOrchestratorTaken) {
			code = http.StatusConflict
		}
		fail(w, code, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSetSessionLabel writes the operator's own name for one session, id
// is the session's transcript UUID (handleDigest's own convention — never
// the daemon's short id). No frontend calls this route yet; see
// Deps.SetSessionLabel's own comment for why.
//
// The field is a pointer for the same reason handlePatchConfig's is: an
// absent key (400) and an explicit empty string are different requests. But
// unlike orchestratorSession, an explicit empty label here is not an error
// answered with 204 by coincidence — it is the one input this route treats
// as a deletion: internal/config.SetSessionLabel removes the session's
// entry entirely rather than persisting an empty string, so a config that
// only ever gained hand-written names never accumulates ones for sessions
// nobody remembers.
func (d Deps) handleSetSessionLabel(w http.ResponseWriter, r *http.Request) {
	if d.SetSessionLabel == nil {
		unavailable(w, "a configuration store")
		return
	}
	var body struct {
		Label *string `json:"label"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Label == nil {
		fail(w, http.StatusBadRequest, "label is required")
		return
	}
	if err := d.SetSessionLabel(r.PathValue("id"), *body.Label); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
