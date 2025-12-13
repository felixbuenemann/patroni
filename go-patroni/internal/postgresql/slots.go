package postgresql

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/patroni/patroni-go/pkg/types"
)

// SlotInfo represents a replication slot.
type SlotInfo struct {
	Name          string
	Type          string
	Database      string
	Plugin        string
	Active        bool
	RestartLSN    int64
	ConfirmedLSN  int64
	CatalogXmin   uint64
	FailsafeLag   int64
}

// SlotsHandler manages replication slots.
type SlotsHandler struct {
	pg   *Postgresql
	mu   sync.RWMutex

	// Cache of known slots
	slots map[string]*SlotInfo
}

// NewSlotsHandler creates a new slots handler.
func NewSlotsHandler(pg *Postgresql) *SlotsHandler {
	return &SlotsHandler{
		pg:    pg,
		slots: make(map[string]*SlotInfo),
	}
}

// GetSlots returns a list of all replication slots.
func (sh *SlotsHandler) GetSlots(ctx context.Context) ([]*SlotInfo, error) {
	query := `
		SELECT
			slot_name,
			slot_type,
			COALESCE(database, ''),
			COALESCE(plugin, ''),
			active,
			COALESCE((restart_lsn - '0/0')::bigint, 0),
			COALESCE((confirmed_flush_lsn - '0/0')::bigint, 0),
			COALESCE(catalog_xmin::bigint, 0)
		FROM pg_catalog.pg_replication_slots
	`

	rows, err := sh.pg.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query slots: %w", err)
	}
	defer rows.Close()

	var slots []*SlotInfo
	sh.mu.Lock()
	defer sh.mu.Unlock()

	sh.slots = make(map[string]*SlotInfo)

	for rows.Next() {
		slot := &SlotInfo{}
		var catalogXmin int64
		err := rows.Scan(
			&slot.Name,
			&slot.Type,
			&slot.Database,
			&slot.Plugin,
			&slot.Active,
			&slot.RestartLSN,
			&slot.ConfirmedLSN,
			&catalogXmin,
		)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to scan slot row")
			continue
		}
		slot.CatalogXmin = uint64(catalogXmin)

		slots = append(slots, slot)
		sh.slots[slot.Name] = slot
	}

	return slots, nil
}

// CreateSlot creates a replication slot.
func (sh *SlotsHandler) CreateSlot(ctx context.Context, name string, slotType string, immediately bool) error {
	log.Info().Str("slot", name).Str("type", slotType).Msg("Creating replication slot")

	var query string
	switch slotType {
	case "physical":
		if immediately {
			query = fmt.Sprintf(
				"SELECT pg_catalog.pg_create_physical_replication_slot(%s, true)",
				quoteIdent(name),
			)
		} else {
			query = fmt.Sprintf(
				"SELECT pg_catalog.pg_create_physical_replication_slot(%s)",
				quoteIdent(name),
			)
		}
	case "logical":
		return fmt.Errorf("logical slot creation requires plugin specification")
	default:
		return fmt.Errorf("unknown slot type: %s", slotType)
	}

	if err := sh.pg.Exec(ctx, query); err != nil {
		return fmt.Errorf("failed to create slot: %w", err)
	}

	return nil
}

// DropSlot drops a replication slot.
func (sh *SlotsHandler) DropSlot(ctx context.Context, name string) error {
	log.Info().Str("slot", name).Msg("Dropping replication slot")

	query := fmt.Sprintf("SELECT pg_catalog.pg_drop_replication_slot(%s)", quoteIdent(name))

	if err := sh.pg.Exec(ctx, query); err != nil {
		return fmt.Errorf("failed to drop slot: %w", err)
	}

	sh.mu.Lock()
	delete(sh.slots, name)
	sh.mu.Unlock()

	return nil
}

// SyncSlots synchronizes replication slots with cluster members.
func (sh *SlotsHandler) SyncSlots(ctx context.Context, members []*types.Member, myName string) error {
	// Get current slots
	currentSlots, err := sh.GetSlots(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current slots: %w", err)
	}

	// Build map of expected slots
	expectedSlots := make(map[string]bool)
	for _, member := range members {
		if member.Name != myName {
			slotName := types.SlotNameFromMemberName(member.Name)
			expectedSlots[slotName] = true
		}
	}

	// Create missing slots
	for slotName := range expectedSlots {
		found := false
		for _, slot := range currentSlots {
			if slot.Name == slotName {
				found = true
				break
			}
		}
		if !found {
			if err := sh.CreateSlot(ctx, slotName, "physical", false); err != nil {
				log.Warn().Err(err).Str("slot", slotName).Msg("Failed to create slot")
			}
		}
	}

	// Drop orphaned slots
	for _, slot := range currentSlots {
		if slot.Type == "physical" && strings.HasPrefix(slot.Name, "patroni_") {
			if !expectedSlots[slot.Name] {
				if err := sh.DropSlot(ctx, slot.Name); err != nil {
					log.Warn().Err(err).Str("slot", slot.Name).Msg("Failed to drop orphaned slot")
				}
			}
		}
	}

	return nil
}

// AdvanceSlot advances a replication slot to a given LSN.
func (sh *SlotsHandler) AdvanceSlot(ctx context.Context, name string, lsn int64) error {
	if sh.pg.MajorVersion() < 110000 {
		return nil // pg_replication_slot_advance not available before PG 11
	}

	query := fmt.Sprintf(
		"SELECT pg_catalog.pg_replication_slot_advance(%s, %s)",
		quoteIdent(name),
		quoteLSN(lsn),
	)

	if err := sh.pg.Exec(ctx, query); err != nil {
		return fmt.Errorf("failed to advance slot: %w", err)
	}

	return nil
}

// GetSlotStatus returns status information for all slots.
func (sh *SlotsHandler) GetSlotStatus(ctx context.Context) map[string]int64 {
	slots, err := sh.GetSlots(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get slots for status")
		return nil
	}

	status := make(map[string]int64)
	for _, slot := range slots {
		status[slot.Name] = slot.RestartLSN
	}

	return status
}

// RunAdvanceLoop periodically advances inactive slots.
func (sh *SlotsHandler) RunAdvanceLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sh.advanceInactiveSlots(ctx)
		}
	}
}

// advanceInactiveSlots advances slots that are not actively being used.
func (sh *SlotsHandler) advanceInactiveSlots(ctx context.Context) {
	if !sh.pg.IsPrimary() {
		return
	}

	slots, err := sh.GetSlots(ctx)
	if err != nil {
		return
	}

	currentLSN := sh.pg.LastOperation()
	if currentLSN == 0 {
		return
	}

	for _, slot := range slots {
		if !slot.Active && slot.Type == "physical" && slot.RestartLSN > 0 {
			// Calculate lag
			lag := currentLSN - slot.RestartLSN
			if lag > 1024*1024*1024 { // 1GB threshold
				log.Info().
					Str("slot", slot.Name).
					Int64("lag", lag).
					Msg("Advancing stale slot")

				if err := sh.AdvanceSlot(ctx, slot.Name, currentLSN); err != nil {
					log.Warn().Err(err).Str("slot", slot.Name).Msg("Failed to advance slot")
				}
			}
		}
	}
}

// quoteIdent quotes an identifier for SQL.
func quoteIdent(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// quoteLSN converts an LSN to PostgreSQL format.
func quoteLSN(lsn int64) string {
	return fmt.Sprintf("'%X/%X'", lsn>>32, lsn&0xFFFFFFFF)
}
