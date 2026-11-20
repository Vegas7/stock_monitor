// Package state is the service's runtime record. Only the service writes it.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/Vegas7/stock_monitor/internal/check"
	"github.com/Vegas7/stock_monitor/internal/fsutil"
	"github.com/Vegas7/stock_monitor/internal/misaka"
)

type State struct {
	Items            map[string]*ItemState `json:"items"`
	Tokens           misaka.Tokens         `json:"tokens"`
	UsedArmIDs       []string              `json:"used_arm_ids"`
	Orders           []OrderRecord         `json:"orders"`
	CleanShutdown    bool                  `json:"clean_shutdown"`
	LastHeartbeat    time.Time             `json:"last_heartbeat"`
	LastVersionCheck time.Time             `json:"last_version_check"`
	LatestVersion    string                `json:"latest_version"`
}

type ItemState struct {
	Stock            check.Status `json:"stock"`
	Detail           string       `json:"detail"`
	ConsecutiveFails int          `json:"consecutive_fails"`
	Warned           bool         `json:"warned"`
	LastWarnAt       time.Time    `json:"last_warn_at"`
	LastCheckAt      time.Time    `json:"last_check_at"`
	LastChangeAt     time.Time    `json:"last_change_at"`
	LastError        string       `json:"last_error"`
}

// Order record statuses.
const (
	OrderPlacing = "placing"
	OrderPlaced  = "placed"
	OrderFailed  = "failed"
	OrderUnknown = "unknown"
)

type OrderRecord struct {
	ItemID    string    `json:"item_id"`
	ArmID     string    `json:"arm_id"`
	Status    string    `json:"status"`
	At        time.Time `json:"at"`
	OrderIDs  []string  `json:"order_ids"`
	InvoiceID string    `json:"invoice_id"`
	Message   string    `json:"message"`
}

// ArmUsed reports whether an arm id has already been spent on an order.
func (s *State) ArmUsed(armID string) bool { return slices.Contains(s.UsedArmIDs, armID) }

func (s *State) normalize() {
	if s.Items == nil {
		s.Items = map[string]*ItemState{}
	}
}

func (s State) clone() State {
	c := s
	c.Items = make(map[string]*ItemState, len(s.Items))
	for k, v := range s.Items {
		if v != nil {
			vv := *v
			c.Items[k] = &vv
		}
	}
	c.UsedArmIDs = slices.Clone(s.UsedArmIDs)
	c.Orders = make([]OrderRecord, len(s.Orders))
	for i, o := range s.Orders {
		o.OrderIDs = slices.Clone(o.OrderIDs)
		c.Orders[i] = o
	}
	return c
}

// Store guards State and persists every update.
type Store struct {
	path string
	mu   sync.Mutex
	s    State
}

// Open loads state from path; a missing file yields empty state.
func Open(path string) (*Store, error) {
	st := &Store{path: path}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &st.s); err != nil {
			return nil, fmt.Errorf("state %s: %w", path, err)
		}
	}
	st.s.normalize()
	return st, nil
}

// Update mutates state under the lock and saves it atomically (0600).
func (st *Store) Update(fn func(*State)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(&st.s)
	st.s.normalize()
	b, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(st.path, b, 0o600)
}

// Snapshot returns a deep copy of the current state.
func (st *Store) Snapshot() State {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.clone()
}

// LoadTokens implements misaka.TokenStore.
func (st *Store) LoadTokens() misaka.Tokens {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.Tokens
}

// SaveTokens implements misaka.TokenStore.
func (st *Store) SaveTokens(t misaka.Tokens) error {
	return st.Update(func(s *State) { s.Tokens = t })
}
