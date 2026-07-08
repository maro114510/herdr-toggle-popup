// Package state implements the popups.json v1 registry: atomic writes, corruption
// recovery, and CRUD keyed by scope-prefixed keys.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// now and pid are overridden in tests to make backup-name collisions deterministic.
var (
	now = time.Now
	pid = os.Getpid
)

const stateDirEnvVar = "HERDR_PLUGIN_STATE_DIR"

// Entry is a single popup registry entry. Field names and types are the
// popups.json v1 wire format — do not change JSON tags without a migration path.
type Entry struct {
	PaneID          string  `json:"pane_id"`
	PluginID        string  `json:"plugin_id"`
	Entrypoint      string  `json:"entrypoint"`
	Scope           string  `json:"scope"`
	WorkspaceID     *string `json:"workspace_id"`
	TabID           *string `json:"tab_id"`
	CreatedAtUnixMs int64   `json:"created_at_unix_ms"`
	Hidden          *bool   `json:"hidden,omitempty"`
}

// Registry is the top-level popups.json document.
type Registry struct {
	Version int              `json:"version"`
	Popups  map[string]Entry `json:"popups"`
}

func defaultRegistry() Registry {
	return Registry{Version: 1, Popups: map[string]Entry{}}
}

// StateDirFromEnv reads HERDR_PLUGIN_STATE_DIR, erroring if it is unset.
func StateDirFromEnv() (string, error) {
	dir := os.Getenv(stateDirEnvVar)
	if dir == "" {
		return "", fmt.Errorf("%s must be set", stateDirEnvVar)
	}
	return dir, nil
}

// Store manages the popups.json registry rooted at a given state directory.
type Store struct {
	dir string
}

// NewStore returns a Store backed by popups.json under dir.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) filePath() string {
	return filepath.Join(s.dir, "popups.json")
}

// decodeRegistry parses data and reports whether it is a valid v1 registry:
// version == 1 and popups is an object (present and non-null).
func decodeRegistry(data []byte) (Registry, bool) {
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return Registry{}, false
	}
	if reg.Version != 1 || reg.Popups == nil {
		return Registry{}, false
	}
	return reg, true
}

// backupPath returns the path popups.json should be moved to when corrupt,
// appending a pid suffix if the timestamp-based name is already taken.
func backupPath(file string) string {
	backup := fmt.Sprintf("%s.bak.%d", file, now().Unix())
	if _, err := os.Stat(backup); err == nil {
		backup = fmt.Sprintf("%s.%d", backup, pid())
	}
	return backup
}

// Read returns the current registry. A missing file yields the default empty
// registry. A corrupt or malformed file is backed up to popups.json.bak.<unix
// ts> (or a pid-suffixed variant on name collision), reinitialized with the
// default registry, which is then returned. If the backup move fails, Read
// errors without resetting the file.
func (s *Store) Read() (Registry, error) {
	file := s.filePath()
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return defaultRegistry(), nil
	}
	if err != nil {
		return Registry{}, err
	}

	if reg, ok := decodeRegistry(data); ok {
		return reg, nil
	}

	backup := backupPath(file)
	if err := os.Rename(file, backup); err != nil {
		return Registry{}, fmt.Errorf("state: failed to back up corrupt registry, aborting reset: %w", err)
	}

	def := defaultRegistry()
	if err := s.WriteRegistry(def); err != nil {
		return Registry{}, err
	}
	return def, nil
}

// WriteRegistry atomically writes reg as the registry: write to a temp file in
// the same directory, then rename it into place. Creates the parent directory
// if missing.
func (s *Store) WriteRegistry(reg Registry) error {
	data, err := json.Marshal(reg)
	if err != nil {
		return err
	}

	file := s.filePath()
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".popups.json.tmp.*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, file); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// Get returns the entry for key. ok is false if the key is not present.
func (s *Store) Get(key string) (entry Entry, ok bool, err error) {
	reg, err := s.Read()
	if err != nil {
		return Entry{}, false, err
	}
	entry, ok = reg.Popups[key]
	return entry, ok, nil
}

// Set stores entry under key, replacing any existing entry. It never carries
// over a previous hidden flag — use SetHidden for that.
func (s *Store) Set(key string, entry Entry) error {
	reg, err := s.Read()
	if err != nil {
		return err
	}
	entry.Hidden = nil
	reg.Popups[key] = entry
	return s.WriteRegistry(reg)
}

// SetHidden sets the hidden flag on the entry for key, leaving every other
// field untouched. It is a no-op, not an error, when key is absent.
func (s *Store) SetHidden(key string, hidden bool) error {
	reg, err := s.Read()
	if err != nil {
		return err
	}
	entry, ok := reg.Popups[key]
	if !ok {
		return nil
	}
	entry.Hidden = &hidden
	reg.Popups[key] = entry
	return s.WriteRegistry(reg)
}

// Delete removes the entry for key, if present. Idempotent.
func (s *Store) Delete(key string) error {
	reg, err := s.Read()
	if err != nil {
		return err
	}
	delete(reg.Popups, key)
	return s.WriteRegistry(reg)
}

// DeleteByPaneID removes every entry whose PaneID matches, regardless of key.
// Idempotent.
func (s *Store) DeleteByPaneID(paneID string) error {
	reg, err := s.Read()
	if err != nil {
		return err
	}
	for key, entry := range reg.Popups {
		if entry.PaneID == paneID {
			delete(reg.Popups, key)
		}
	}
	return s.WriteRegistry(reg)
}
