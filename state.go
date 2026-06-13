package dctl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// HomeRef points at the category or forum that holds session channels.
type HomeRef struct {
	ID   string `json:"id"`
	Type string `json:"type"` // "category" | "forum"
}

// Session is one bridged channel/post supervised by the daemon.
type Session struct {
	Name      string `json:"name"`
	ChannelID string `json:"channelID"`
	Type      string `json:"type"` // "text" | "forum"
	Cmd       string `json:"cmd"`
	Worktree  string `json:"worktree,omitempty"` // abs path; empty for a shared session
}

// State is the daemon's persisted configuration. All access is mutex-guarded.
type State struct {
	mu              sync.Mutex `json:"-"`
	path            string     `json:"-"`
	Home            HomeRef    `json:"home"`
	Allow           []string   `json:"allow"`
	Repo            string     `json:"repo,omitempty"` // project sessions operate on; defaults to daemon cwd
	Sessions        []Session  `json:"sessions"`
	StatusMessageID string     `json:"statusMessageID,omitempty"` // cached id of the status embed
}

// NewState returns an empty state bound to path (not yet written).
func NewState(path string) *State { return &State{path: path} }

// LoadState reads state from path; a missing file yields an empty state.
func LoadState(path string) (*State, error) {
	s := NewState(path)
	buf, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(buf, s); err != nil {
		return nil, err
	}
	return s, nil
}

// Save atomically writes state to its path.
func (s *State) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *State) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Allowed reports whether userID may invoke commands.
func (s *State) Allowed(userID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.Allow {
		if id == userID {
			return true
		}
	}
	return false
}

// AddAllow adds userID to the allowlist (idempotent) and persists.
func (s *State) AddAllow(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.Allow {
		if id == userID {
			return nil
		}
	}
	s.Allow = append(s.Allow, userID)
	return s.saveLocked()
}

// RemoveAllow removes userID from the allowlist and persists.
func (s *State) RemoveAllow(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.Allow[:0]
	for _, id := range s.Allow {
		if id != userID {
			out = append(out, id)
		}
	}
	s.Allow = out
	return s.saveLocked()
}

// FindSession returns the session with name (and whether it exists).
func (s *State) FindSession(name string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ss := range s.Sessions {
		if ss.Name == name {
			return ss, true
		}
	}
	return Session{}, false
}

// AddSession adds a session, erroring if the name is taken, and persists.
func (s *State) AddSession(sess Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ss := range s.Sessions {
		if ss.Name == sess.Name {
			return fmt.Errorf("session %q already exists", sess.Name)
		}
	}
	s.Sessions = append(s.Sessions, sess)
	return s.saveLocked()
}

// RemoveSession drops the session named name and persists.
func (s *State) RemoveSession(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.Sessions[:0]
	for _, ss := range s.Sessions {
		if ss.Name != name {
			out = append(out, ss)
		}
	}
	s.Sessions = out
	return s.saveLocked()
}

// SetHome records the home ref and persists.
func (s *State) SetHome(h HomeRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Home = h
	return s.saveLocked()
}

// SetStatusMessageID caches the status embed's message id and persists.
func (s *State) SetStatusMessageID(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.StatusMessageID = id
	return s.saveLocked()
}

// SnapshotSessions returns a copy of the current sessions.
func (s *State) SnapshotSessions() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Session(nil), s.Sessions...)
}
