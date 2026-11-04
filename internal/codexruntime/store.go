package codexruntime

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kroticw/fleetdeck/internal/executor"
	"golang.org/x/sys/unix"
)

const ProtocolVersion = 1
const maxRecordSize = 1 << 20

var (
	ErrPrivateState      = errors.New("runtime state must be private and owned by the current user")
	ErrInvalidRecord     = errors.New("invalid runtime record")
	ErrIncompatible      = errors.New("incompatible runtime protocol")
	ErrDurabilityUnknown = errors.New("runtime record published but durability is unknown")
)

// Record is recovery metadata, not proof that a process is alive. A matching
// socket handshake is required before using PID or issuing any command.
// Prompts, output, environment values and credentials do not belong here.
type Record struct {
	Version           int                  `json:"version"`
	Key               executor.Key         `json:"key"`
	Nonce             string               `json:"nonce"`
	PID               int                  `json:"pid"`
	ProcessStartedAt  string               `json:"processStartedAt,omitempty"`
	ThreadID          string               `json:"threadId,omitempty"`
	RootSessionID     string               `json:"rootSessionId,omitempty"`
	CWD               string               `json:"cwd"`
	Board             string               `json:"board"`
	Card              string               `json:"card"`
	ConfigPath        string               `json:"configPath,omitempty"`
	LaunchFingerprint string               `json:"launchFingerprint,omitempty"`
	Profile           string               `json:"profile,omitempty"`
	Model             string               `json:"model,omitempty"`
	Sandbox           string               `json:"sandbox,omitempty"`
	ApprovalPolicy    string               `json:"approvalPolicy,omitempty"`
	Lifecycle         string               `json:"lifecycle"`
	CreatedAt         time.Time            `json:"createdAt"`
	Operations        map[string]Operation `json:"operations,omitempty"`
}

// Operation stores delivery identity and phase without persisting message text.
type Operation struct {
	Kind      string                 `json:"kind"`
	Phase     string                 `json:"phase"`
	TurnID    string                 `json:"turnId,omitempty"`
	MessageID string                 `json:"messageId,omitempty"`
	Receipt   executor.DeliveryState `json:"receipt,omitempty"`
}

type Store struct {
	root   *os.Root
	rename func(string, string) error
}

func OpenStore(dir string) (*Store, error) {
	if !filepath.IsAbs(dir) {
		return nil, ErrPrivateState
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !privateFile(info, true) {
		return nil, ErrPrivateState
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	held, err := root.Stat(".")
	if err != nil || !os.SameFile(info, held) || !privateFile(held, true) {
		_ = root.Close()
		return nil, ErrPrivateState
	}
	return &Store{root: root, rename: root.Rename}, nil
}

func (s *Store) Close() error { return s.root.Close() }

func privateFile(info os.FileInfo, dir bool) bool {
	if info == nil || info.IsDir() != dir || (!dir && !info.Mode().IsRegular()) || info.Mode().Perm()&0o077 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}

func recordName(raw string) (executor.Key, string, error) {
	id, key, err := executor.ParseKey(raw)
	if err != nil || id != executor.Codex {
		return "", "", executor.ErrInvalidKey
	}
	return key, strings.TrimPrefix(string(key), "codex:"), nil
}

func (s *Store) openPrivate(name string, flags int) (*os.File, error) {
	f, err := s.root.OpenFile(name, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !privateFile(info, false) {
		_ = f.Close()
		return nil, ErrPrivateState
	}
	return f, nil
}

func (s *Store) Read(raw string) (Record, error) {
	key, name, err := recordName(raw)
	if err != nil {
		return Record{}, err
	}
	f, err := s.openPrivate(name+".json", os.O_RDONLY)
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, maxRecordSize+1))
	if err != nil {
		return Record{}, err
	}
	if len(body) > maxRecordSize {
		return Record{}, ErrInvalidRecord
	}
	var record Record
	if json.Unmarshal(body, &record) != nil {
		return Record{}, ErrInvalidRecord
	}
	if err := validRecord(record, key); err != nil {
		return Record{}, err
	}
	return record, nil
}

func validRecord(r Record, key executor.Key) error {
	if r.Version != ProtocolVersion {
		return ErrIncompatible
	}
	if r.Key != key || len(r.Nonce) < 32 || r.PID < 1 || !filepath.IsAbs(r.CWD) || !filepath.IsAbs(r.Board) || !filepath.IsAbs(r.Card) || r.CreatedAt.IsZero() {
		return ErrInvalidRecord
	}
	rel, err := filepath.Rel(r.Board, r.Card)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrInvalidRecord
	}
	switch r.Lifecycle {
	case "starting", "live", "stopped", "dead", "unknown":
	default:
		return ErrInvalidRecord
	}
	return nil
}

// Owner holds the kernel lock for one runtime key. Another process cannot
// resume the same key until this handle closes or its process exits.
type Owner struct {
	store  *Store
	key    executor.Key
	name   string
	file   *os.File
	mu     sync.Mutex
	closed bool
}

func (s *Store) Acquire(raw executor.Key) (*Owner, error) {
	key, name, err := recordName(string(raw))
	if err != nil {
		return nil, err
	}
	f, err := s.openPrivate(name+".lock", os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, executor.ErrConflict
		}
		return nil, err
	}
	return &Owner{store: s, key: key, name: name, file: f}, nil
}

func (o *Owner) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	o.closed = true
	// Closing the descriptor releases flock, including on process death.
	return o.file.Close()
}

func (o *Owner) Save(record Record) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return executor.ErrConflict
	}
	if err := validRecord(record, o.key); err != nil {
		return err
	}
	body, err := json.Marshal(record)
	if err != nil {
		return ErrInvalidRecord
	}
	if len(body)+1 > maxRecordSize {
		return ErrInvalidRecord
	}
	s := o.store
	// Refuse an exposed or symlinked destination even though Rename itself
	// would replace its directory entry without following the target.
	if f, err := s.openPrivate(o.name+".json", os.O_RDONLY); err == nil {
		if err := f.Close(); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp := ".record-" + rand.Text()
	f, err := s.openPrivate(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		return err
	}
	defer func() { _ = s.root.Remove(tmp) }()
	if _, err := f.Write(append(body, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := s.rename(tmp, o.name+".json"); err != nil {
		return err
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return fmt.Errorf("%w: cannot open directory", ErrDurabilityUnknown)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		return ErrDurabilityUnknown
	}
	return nil
}
