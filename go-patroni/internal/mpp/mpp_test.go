package mpp

import (
	"context"
	"testing"
)

func TestTypeConstants(t *testing.T) {
	// Verify type constants match expected values
	types := map[Type]string{
		TypeNull:  "null",
		TypeCitus: "citus",
	}

	for typ, expected := range types {
		if string(typ) != expected {
			t.Errorf("Type %v = %q, want %q", typ, string(typ), expected)
		}
	}
}

func TestCoordinatorGroupIDConstant(t *testing.T) {
	if CoordinatorGroupID != 0 {
		t.Errorf("CoordinatorGroupID = %d, want 0", CoordinatorGroupID)
	}
}

func TestGetHandler(t *testing.T) {
	tests := []struct {
		name         string
		config       *Config
		expectedType Type
	}{
		{
			name:         "nil config",
			config:       nil,
			expectedType: TypeNull,
		},
		{
			name:         "empty config",
			config:       &Config{},
			expectedType: TypeNull,
		},
		{
			name:         "null type",
			config:       &Config{Type: TypeNull},
			expectedType: TypeNull,
		},
		{
			name:         "citus type",
			config:       &Config{Type: TypeCitus},
			expectedType: TypeCitus,
		},
		{
			name:         "unknown type",
			config:       &Config{Type: "unknown"},
			expectedType: TypeNull,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, err := GetHandler(tt.config)
			if err != nil {
				t.Fatalf("GetHandler() error = %v", err)
			}
			if handler == nil {
				t.Fatal("GetHandler() returned nil")
			}
			if handler.Type() != tt.expectedType {
				t.Errorf("Type() = %v, want %v", handler.Type(), tt.expectedType)
			}
		})
	}
}

func TestGetHandlerFromMap(t *testing.T) {
	tests := []struct {
		name         string
		config       map[string]interface{}
		expectedType Type
	}{
		{
			name:         "nil config",
			config:       nil,
			expectedType: TypeNull,
		},
		{
			name:         "empty config",
			config:       map[string]interface{}{},
			expectedType: TypeNull,
		},
		{
			name: "citus config",
			config: map[string]interface{}{
				"citus": map[string]interface{}{
					"group":    0,
					"database": "citus",
				},
			},
			expectedType: TypeCitus,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, err := GetHandlerFromMap(tt.config)
			if err != nil {
				t.Fatalf("GetHandlerFromMap() error = %v", err)
			}
			if handler == nil {
				t.Fatal("GetHandlerFromMap() returned nil")
			}
			if handler.Type() != tt.expectedType {
				t.Errorf("Type() = %v, want %v", handler.Type(), tt.expectedType)
			}
		})
	}
}

// Test matches Python test_null_handler
func TestNullHandler(t *testing.T) {
	handler := NewNullHandler()

	if handler == nil {
		t.Fatal("NewNullHandler() returned nil")
	}

	// Verify type
	if handler.Type() != TypeNull {
		t.Errorf("Type() = %v, want %v", handler.Type(), TypeNull)
	}

	// Verify group is nil (matches Python: self.assertIsNone(mpp.group))
	if handler.Group() != nil {
		t.Error("Group() should return nil")
	}

	// Verify coordinator group ID is -1
	if handler.CoordinatorGroupID() != -1 {
		t.Errorf("CoordinatorGroupID() = %d, want -1", handler.CoordinatorGroupID())
	}

	// Verify not coordinator or worker
	if handler.IsCoordinator() {
		t.Error("IsCoordinator() should return false")
	}
	if handler.IsWorker() {
		t.Error("IsWorker() should return false")
	}

	ctx := context.Background()

	// Test all methods return nil/no error (matches Python assertions)
	if err := handler.HandleEvent(ctx, "test_event"); err != nil {
		t.Errorf("HandleEvent() error = %v, want nil", err)
	}

	if err := handler.SyncMetaData(ctx); err != nil {
		t.Errorf("SyncMetaData() error = %v, want nil", err)
	}

	if err := handler.OnDemote(ctx); err != nil {
		t.Errorf("OnDemote() error = %v, want nil", err)
	}

	if err := handler.ScheduleCacheRebuild(ctx); err != nil {
		t.Errorf("ScheduleCacheRebuild() error = %v, want nil", err)
	}

	if err := handler.Bootstrap(ctx); err != nil {
		t.Errorf("Bootstrap() error = %v, want nil", err)
	}

	// Test AdjustPostgresGUCs returns input unchanged
	gucs := map[string]interface{}{"max_connections": 100}
	result := handler.AdjustPostgresGUCs(ctx, gucs)
	if result["max_connections"] != 100 {
		t.Error("AdjustPostgresGUCs() should return gucs unchanged")
	}

	// Test IgnoreReplicationSlot returns false (matches Python assertion)
	if handler.IgnoreReplicationSlot(map[string]interface{}{"name": "test_slot"}) {
		t.Error("IgnoreReplicationSlot() should return false")
	}

	// Test ValidateConfig always returns nil
	if err := handler.ValidateConfig(map[string]interface{}{}); err != nil {
		t.Errorf("ValidateConfig() error = %v, want nil", err)
	}
}

func TestCitusHandler(t *testing.T) {
	group := 0
	config := &Config{
		Type:     TypeCitus,
		Group:    &group,
		Database: "citus",
	}
	handler := NewCitusHandler(config)

	if handler == nil {
		t.Fatal("NewCitusHandler() returned nil")
	}

	// Verify type
	if handler.Type() != TypeCitus {
		t.Errorf("Type() = %v, want %v", handler.Type(), TypeCitus)
	}

	// Verify group
	if handler.Group() == nil {
		t.Error("Group() should not be nil")
	} else if *handler.Group() != 0 {
		t.Errorf("Group() = %d, want 0", *handler.Group())
	}

	// Verify coordinator group ID
	if handler.CoordinatorGroupID() != 0 {
		t.Errorf("CoordinatorGroupID() = %d, want 0", handler.CoordinatorGroupID())
	}

	// Group 0 should be coordinator
	if !handler.IsCoordinator() {
		t.Error("IsCoordinator() should return true for group 0")
	}
	if handler.IsWorker() {
		t.Error("IsWorker() should return false for group 0")
	}
}

func TestCitusHandlerWorker(t *testing.T) {
	group := 1
	config := &Config{
		Type:  TypeCitus,
		Group: &group,
	}
	handler := NewCitusHandler(config)

	// Group > 0 should be worker
	if handler.IsCoordinator() {
		t.Error("IsCoordinator() should return false for group > 0")
	}
	if !handler.IsWorker() {
		t.Error("IsWorker() should return true for group > 0")
	}
}

func TestCitusHandlerNilGroup(t *testing.T) {
	config := &Config{
		Type:  TypeCitus,
		Group: nil,
	}
	handler := NewCitusHandler(config)

	if handler.Group() != nil {
		t.Error("Group() should return nil when not configured")
	}
	if handler.IsCoordinator() {
		t.Error("IsCoordinator() should return false when group is nil")
	}
	if handler.IsWorker() {
		t.Error("IsWorker() should return false when group is nil")
	}
}

func TestCitusHandlerMethods(t *testing.T) {
	group := 0
	config := &Config{
		Type:  TypeCitus,
		Group: &group,
	}
	handler := NewCitusHandler(config)
	ctx := context.Background()

	// All methods should succeed without error
	if err := handler.HandleEvent(ctx, "test_event"); err != nil {
		t.Errorf("HandleEvent() error = %v", err)
	}

	if err := handler.SyncMetaData(ctx); err != nil {
		t.Errorf("SyncMetaData() error = %v", err)
	}

	if err := handler.OnDemote(ctx); err != nil {
		t.Errorf("OnDemote() error = %v", err)
	}

	if err := handler.ScheduleCacheRebuild(ctx); err != nil {
		t.Errorf("ScheduleCacheRebuild() error = %v", err)
	}

	if err := handler.Bootstrap(ctx); err != nil {
		t.Errorf("Bootstrap() error = %v", err)
	}

	if err := handler.ValidateConfig(map[string]interface{}{}); err != nil {
		t.Errorf("ValidateConfig() error = %v", err)
	}
}

func TestCitusHandlerAdjustPostgresGUCs(t *testing.T) {
	group := 0
	config := &Config{
		Type:  TypeCitus,
		Group: &group,
	}
	handler := NewCitusHandler(config)
	ctx := context.Background()

	tests := []struct {
		name     string
		input    map[string]interface{}
		expected string
	}{
		{
			name:     "nil gucs",
			input:    nil,
			expected: "citus",
		},
		{
			name:     "empty gucs",
			input:    map[string]interface{}{},
			expected: "citus",
		},
		{
			name:     "empty shared_preload_libraries",
			input:    map[string]interface{}{"shared_preload_libraries": ""},
			expected: "citus",
		},
		{
			name:     "existing library without citus",
			input:    map[string]interface{}{"shared_preload_libraries": "pg_stat_statements"},
			expected: "pg_stat_statements,citus",
		},
		{
			name:     "citus already present",
			input:    map[string]interface{}{"shared_preload_libraries": "citus"},
			expected: "citus",
		},
		{
			name:     "citus with other libraries",
			input:    map[string]interface{}{"shared_preload_libraries": "pg_stat_statements,citus"},
			expected: "pg_stat_statements,citus",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.AdjustPostgresGUCs(ctx, tt.input)
			spl := result["shared_preload_libraries"].(string)
			if spl != tt.expected {
				t.Errorf("shared_preload_libraries = %q, want %q", spl, tt.expected)
			}
		})
	}
}

func TestCitusHandlerIgnoreReplicationSlot(t *testing.T) {
	group := 0
	config := &Config{
		Type:  TypeCitus,
		Group: &group,
	}
	handler := NewCitusHandler(config)

	tests := []struct {
		name   string
		slot   map[string]interface{}
		ignore bool
	}{
		{
			name:   "normal slot",
			slot:   map[string]interface{}{"name": "my_slot"},
			ignore: false,
		},
		{
			name:   "citus internal slot",
			slot:   map[string]interface{}{"name": "citus_shard_move_slot"},
			ignore: true,
		},
		{
			name:   "citus prefix",
			slot:   map[string]interface{}{"name": "citus_12345"},
			ignore: true,
		},
		{
			name:   "no name",
			slot:   map[string]interface{}{},
			ignore: false,
		},
		{
			name:   "non-string name",
			slot:   map[string]interface{}{"name": 123},
			ignore: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.IgnoreReplicationSlot(tt.slot)
			if result != tt.ignore {
				t.Errorf("IgnoreReplicationSlot() = %v, want %v", result, tt.ignore)
			}
		})
	}
}

func TestContainsCitus(t *testing.T) {
	tests := []struct {
		name      string
		libraries string
		contains  bool
	}{
		{"empty", "", false},
		{"only citus", "citus", true},
		{"citus first", "citus,pg_stat_statements", true},
		{"citus last", "pg_stat_statements,citus", true},
		{"citus middle", "pg_stat_statements,citus,auto_explain", true},
		{"no citus", "pg_stat_statements,auto_explain", false},
		{"partial match", "mycitus", false},
		{"with spaces", "pg_stat_statements, citus", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsCitus(tt.libraries)
			if result != tt.contains {
				t.Errorf("containsCitus(%q) = %v, want %v", tt.libraries, result, tt.contains)
			}
		})
	}
}

func TestSplitLibraries(t *testing.T) {
	tests := []struct {
		name      string
		libraries string
		count     int
	}{
		{"empty", "", 0},
		{"single", "citus", 1},
		{"two", "citus,pg_stat_statements", 2},
		{"with spaces", " citus , pg_stat_statements ", 2},
		{"empty entries", "citus,,pg_stat_statements", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitLibraries(tt.libraries)
			if len(result) != tt.count {
				t.Errorf("splitLibraries(%q) = %d items, want %d", tt.libraries, len(result), tt.count)
			}
		})
	}
}

func TestConfigStruct(t *testing.T) {
	group := 1
	cfg := Config{
		Type:     TypeCitus,
		Group:    &group,
		Database: "postgres",
		Options: map[string]interface{}{
			"ssl": true,
		},
	}

	if cfg.Type != TypeCitus {
		t.Errorf("Type = %v, want %v", cfg.Type, TypeCitus)
	}
	if cfg.Group == nil || *cfg.Group != 1 {
		t.Error("Group should be 1")
	}
	if cfg.Database != "postgres" {
		t.Errorf("Database = %q, want %q", cfg.Database, "postgres")
	}
	if cfg.Options["ssl"] != true {
		t.Error("Options[ssl] should be true")
	}
}

func TestHandlerInterface(t *testing.T) {
	// Verify both handlers implement the Handler interface
	var _ Handler = (*NullHandler)(nil)
	var _ Handler = (*CitusHandler)(nil)
}

func TestCitusWorkerSyncMetaData(t *testing.T) {
	// Worker nodes should not sync metadata
	group := 1
	config := &Config{
		Type:  TypeCitus,
		Group: &group,
	}
	handler := NewCitusHandler(config)
	ctx := context.Background()

	// Should return nil without error
	if err := handler.SyncMetaData(ctx); err != nil {
		t.Errorf("SyncMetaData() error = %v", err)
	}
}

// Benchmark tests
func BenchmarkGetHandler(b *testing.B) {
	config := &Config{Type: TypeCitus}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GetHandler(config)
	}
}

func BenchmarkContainsCitus(b *testing.B) {
	libraries := "pg_stat_statements,citus,auto_explain"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		containsCitus(libraries)
	}
}

func BenchmarkAdjustPostgresGUCs(b *testing.B) {
	group := 0
	config := &Config{Type: TypeCitus, Group: &group}
	handler := NewCitusHandler(config)
	ctx := context.Background()
	gucs := map[string]interface{}{"shared_preload_libraries": "pg_stat_statements"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.AdjustPostgresGUCs(ctx, gucs)
	}
}
