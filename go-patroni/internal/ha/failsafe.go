package ha

import (
	"sync"
	"time"

	"github.com/patroni/patroni-go/internal/dcs"
	"github.com/patroni/patroni-go/pkg/types"
)

// Failsafe implements the failsafe mode for split-brain prevention.
type Failsafe struct {
	mu  sync.RWMutex
	dcs dcs.DCS

	lastUpdate time.Time
	name       string
	connURL    string
	apiURL     string
	slots      map[string]int64
}

// NewFailsafe creates a new Failsafe instance.
func NewFailsafe(d dcs.DCS) *Failsafe {
	return &Failsafe{
		dcs:   d,
		slots: make(map[string]int64),
	}
}

// Update updates the failsafe state from a REST API call.
func (f *Failsafe) Update(data map[string]interface{}) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.lastUpdate = time.Now()

	if name, ok := data["name"].(string); ok {
		f.name = name
	}
	if connURL, ok := data["conn_url"].(string); ok {
		f.connURL = connURL
	}
	if apiURL, ok := data["api_url"].(string); ok {
		f.apiURL = apiURL
	}
	if slots, ok := data["slots"].(map[string]interface{}); ok {
		f.slots = make(map[string]int64)
		for k, v := range slots {
			if lsn, ok := v.(float64); ok {
				f.slots[k] = int64(lsn)
			}
		}
	}
}

// UpdateSlots updates the slot information.
func (f *Failsafe) UpdateSlots(slots map[string]int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slots = slots
}

// IsActive returns true if failsafe mode is currently active.
func (f *Failsafe) IsActive() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	// Failsafe is active if we received an update within the TTL
	ttl := 30 * time.Second // Default TTL
	return time.Since(f.lastUpdate) < ttl
}

// GetLeader returns the leader information if failsafe mode is active.
func (f *Failsafe) GetLeader() *types.Leader {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if !f.IsActive() {
		return nil
	}

	member := &types.Member{
		Name: f.name,
		Data: types.MemberData{
			ConnURL: f.connURL,
			APIURL:  f.apiURL,
		},
	}

	return &types.Leader{
		MemberName: f.name,
		Member:     member,
	}
}

// Reset resets the failsafe state.
func (f *Failsafe) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.lastUpdate = time.Time{}
	f.name = ""
	f.connURL = ""
	f.apiURL = ""
	f.slots = make(map[string]int64)
}
