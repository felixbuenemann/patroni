package validate

import (
	"testing"
)

func TestValidatorRequired(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		name  string
		value interface{}
		valid bool
	}{
		{"non-empty string", "hello", true},
		{"empty string", "", false},
		{"non-zero int", 42, true},
		{"zero int", 0, false},
		{"nil", nil, false},
		{"non-empty slice", []string{"a"}, true},
		{"empty slice", []string{}, false},
		{"bool true", true, true},
		{"bool false", false, true}, // bool is never "empty"
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v.Reset()
			result := v.Required("field", tt.value)
			if result != tt.valid {
				t.Errorf("Required(%v) = %v, want %v", tt.value, result, tt.valid)
			}
		})
	}
}

func TestValidatorMinLength(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		value string
		min   int
		valid bool
	}{
		{"hello", 3, true},
		{"hi", 3, false},
		{"hello", 5, true},
		{"hello", 6, false},
		{"", 0, true},
		{"", 1, false},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			v.Reset()
			result := v.MinLength("field", tt.value, tt.min)
			if result != tt.valid {
				t.Errorf("MinLength(%q, %d) = %v, want %v", tt.value, tt.min, result, tt.valid)
			}
		})
	}
}

func TestValidatorMaxLength(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		value string
		max   int
		valid bool
	}{
		{"hello", 10, true},
		{"hello", 5, true},
		{"hello", 4, false},
		{"", 0, true},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			v.Reset()
			result := v.MaxLength("field", tt.value, tt.max)
			if result != tt.valid {
				t.Errorf("MaxLength(%q, %d) = %v, want %v", tt.value, tt.max, result, tt.valid)
			}
		})
	}
}

func TestValidatorRange(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		value int
		min   int
		max   int
		valid bool
	}{
		{5, 1, 10, true},
		{1, 1, 10, true},
		{10, 1, 10, true},
		{0, 1, 10, false},
		{11, 1, 10, false},
		{-5, -10, 0, true},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			v.Reset()
			result := v.Range("field", tt.value, tt.min, tt.max)
			if result != tt.valid {
				t.Errorf("Range(%d, %d, %d) = %v, want %v", tt.value, tt.min, tt.max, result, tt.valid)
			}
		})
	}
}

func TestValidatorPattern(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		value   string
		pattern string
		valid   bool
	}{
		{"hello", `^[a-z]+$`, true},
		{"Hello", `^[a-z]+$`, false},
		{"test123", `^[a-z0-9]+$`, true},
		{"node-1", `^[a-z0-9-]+$`, true},
		{"node_1", `^[a-z0-9_]+$`, true},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			v.Reset()
			result := v.Pattern("field", tt.value, tt.pattern, "test pattern")
			if result != tt.valid {
				t.Errorf("Pattern(%q, %q) = %v, want %v", tt.value, tt.pattern, result, tt.valid)
			}
		})
	}
}

func TestValidatorOneOf(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		value   interface{}
		allowed []interface{}
		valid   bool
	}{
		{"a", []interface{}{"a", "b", "c"}, true},
		{"d", []interface{}{"a", "b", "c"}, false},
		{1, []interface{}{1, 2, 3}, true},
		{4, []interface{}{1, 2, 3}, false},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			v.Reset()
			result := v.OneOf("field", tt.value, tt.allowed...)
			if result != tt.valid {
				t.Errorf("OneOf(%v, %v) = %v, want %v", tt.value, tt.allowed, result, tt.valid)
			}
		})
	}
}

func TestValidatorValidHost(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		value string
		valid bool
	}{
		{"localhost", true},
		{"example.com", true},
		{"sub.example.com", true},
		{"192.168.1.1", true},
		{"10.0.0.1", true},
		{"::1", true},
		{"", true}, // Empty is valid (optional)
		{"invalid..host", false},
		{"-invalid", false},
		{"invalid-", false},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			v.Reset()
			result := v.ValidHost("field", tt.value)
			if result != tt.valid {
				t.Errorf("ValidHost(%q) = %v, want %v", tt.value, result, tt.valid)
			}
		})
	}
}

func TestValidatorValidPort(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		port  int
		valid bool
	}{
		{1, true},
		{80, true},
		{443, true},
		{5432, true},
		{8080, true},
		{65535, true},
		{0, false},
		{-1, false},
		{65536, false},
		{100000, false},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			v.Reset()
			result := v.ValidPort("field", tt.port)
			if result != tt.valid {
				t.Errorf("ValidPort(%d) = %v, want %v", tt.port, result, tt.valid)
			}
		})
	}
}

func TestValidatorValidHostPort(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		value string
		valid bool
	}{
		{"localhost:5432", true},
		{"192.168.1.1:5432", true},
		{"example.com:8080", true},
		{"[::1]:5432", true},
		{"", true}, // Empty is valid (optional)
		{"localhost", false},
		{"localhost:", false},
		{":5432", true}, // Empty host is allowed (binds to all interfaces)
		{"localhost:abc", false},
		{"localhost:0", false},
		{"localhost:65536", false},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			v.Reset()
			result := v.ValidHostPort("field", tt.value)
			if result != tt.valid {
				t.Errorf("ValidHostPort(%q) = %v, want %v", tt.value, result, tt.valid)
			}
		})
	}
}

func TestValidatorValidURL(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		value string
		valid bool
	}{
		{"http://example.com", true},
		{"https://example.com", true},
		{"http://localhost:8080", true},
		{"https://user:pass@example.com/path", true},
		{"", true}, // Empty is valid (optional)
		{"example.com", false},
		{"://example.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			v.Reset()
			result := v.ValidURL("field", tt.value)
			if result != tt.valid {
				t.Errorf("ValidURL(%q) = %v, want %v", tt.value, result, tt.valid)
			}
		})
	}
}

func TestValidatorValidIdentifier(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		value string
		valid bool
	}{
		{"node1", true},
		{"my_table", true},
		{"_private", true},
		{"CamelCase", true},
		{"", true}, // Empty is valid (optional)
		{"123start", false},
		{"with-dash", false},
		{"with.dot", false},
		{"with space", false},
		{"select", false}, // Reserved word
		{"table", false},  // Reserved word
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			v.Reset()
			result := v.ValidIdentifier("field", tt.value)
			if result != tt.valid {
				t.Errorf("ValidIdentifier(%q) = %v, want %v", tt.value, result, tt.valid)
			}
		})
	}
}

func TestValidatorValidDuration(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		value string
		valid bool
	}{
		{"30s", true},
		{"5m", true},
		{"1h", true},
		{"1h30m", true},
		{"500ms", true},
		{"", true}, // Empty is valid (optional)
		{"30", false},
		{"invalid", false},
		{"5x", false},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			v.Reset()
			result := v.ValidDuration("field", tt.value)
			if result != tt.valid {
				t.Errorf("ValidDuration(%q) = %v, want %v", tt.value, result, tt.valid)
			}
		})
	}
}

func TestValidationErrors(t *testing.T) {
	v := NewValidator()

	v.Required("field1", "")
	v.Required("field2", "")
	v.MinLength("field3", "ab", 5)

	if !v.HasErrors() {
		t.Error("HasErrors() should return true")
	}

	errors := v.Errors()
	if len(errors) != 3 {
		t.Errorf("Errors count = %d, want 3", len(errors))
	}

	// Test error string
	errStr := errors.Error()
	if errStr == "" {
		t.Error("Error string should not be empty")
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]interface{}
		wantErr bool
	}{
		{
			name: "valid config",
			config: map[string]interface{}{
				"scope": "mycluster",
				"name":  "node1",
				"etcd3": map[string]interface{}{
					"hosts": []interface{}{"localhost:2379"},
				},
				"postgresql": map[string]interface{}{
					"data_dir": "/var/lib/postgresql/14/main",
					"listen":   "0.0.0.0:5432",
				},
			},
			wantErr: false,
		},
		{
			name: "missing scope",
			config: map[string]interface{}{
				"name": "node1",
				"etcd3": map[string]interface{}{
					"hosts": []interface{}{"localhost:2379"},
				},
				"postgresql": map[string]interface{}{
					"data_dir": "/var/lib/postgresql/14/main",
				},
			},
			wantErr: true,
		},
		{
			name: "missing name",
			config: map[string]interface{}{
				"scope": "mycluster",
				"etcd3": map[string]interface{}{
					"hosts": []interface{}{"localhost:2379"},
				},
				"postgresql": map[string]interface{}{
					"data_dir": "/var/lib/postgresql/14/main",
				},
			},
			wantErr: true,
		},
		{
			name: "missing dcs",
			config: map[string]interface{}{
				"scope": "mycluster",
				"name":  "node1",
				"postgresql": map[string]interface{}{
					"data_dir": "/var/lib/postgresql/14/main",
				},
			},
			wantErr: true,
		},
		{
			name: "missing postgresql",
			config: map[string]interface{}{
				"scope": "mycluster",
				"name":  "node1",
				"etcd3": map[string]interface{}{
					"hosts": []interface{}{"localhost:2379"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestIsEmpty(t *testing.T) {
	tests := []struct {
		value    interface{}
		expected bool
	}{
		{nil, true},
		{"", true},
		{"hello", false},
		{0, true},
		{42, false},
		{int64(0), true},
		{int64(42), false},
		{float64(0), true},
		{float64(3.14), false},
		{true, false},
		{false, false}, // bool is never empty
		{[]string{}, true},
		{[]string{"a"}, false},
		{map[string]interface{}{}, true},
		{map[string]interface{}{"a": 1}, false},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := isEmpty(tt.value)
			if result != tt.expected {
				t.Errorf("isEmpty(%v) = %v, want %v", tt.value, result, tt.expected)
			}
		})
	}
}

func TestIsValidHostname(t *testing.T) {
	tests := []struct {
		hostname string
		expected bool
	}{
		{"localhost", true},
		{"example.com", true},
		{"sub.example.com", true},
		{"a.b.c.d.example.com", true},
		{"node-1", true},
		{"node1", true},
		{"-invalid", false},
		{"invalid-", false},
		{"invalid..double", false},
		{"", false},
		{"a", true},
		{"1234567890123456789012345678901234567890123456789012345678901234", false}, // 64 chars label
		{"123456789012345678901234567890123456789012345678901234567890123", true},   // 63 chars label
	}

	for _, tt := range tests {
		t.Run(tt.hostname, func(t *testing.T) {
			result := isValidHostname(tt.hostname)
			if result != tt.expected {
				t.Errorf("isValidHostname(%q) = %v, want %v", tt.hostname, result, tt.expected)
			}
		})
	}
}

func TestIsAlphanumeric(t *testing.T) {
	tests := []struct {
		char     byte
		expected bool
	}{
		{'a', true},
		{'z', true},
		{'A', true},
		{'Z', true},
		{'0', true},
		{'9', true},
		{'-', false},
		{'_', false},
		{'.', false},
		{' ', false},
	}

	for _, tt := range tests {
		t.Run(string(tt.char), func(t *testing.T) {
			result := isAlphanumeric(tt.char)
			if result != tt.expected {
				t.Errorf("isAlphanumeric(%q) = %v, want %v", tt.char, result, tt.expected)
			}
		})
	}
}
