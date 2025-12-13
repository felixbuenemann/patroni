// Package validate provides comprehensive validation for Patroni configuration and state.
package validate

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ValidationError represents a validation error.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidationErrors is a collection of validation errors.
type ValidationErrors []*ValidationError

func (e ValidationErrors) Error() string {
	var messages []string
	for _, err := range e {
		messages = append(messages, err.Error())
	}
	return strings.Join(messages, "; ")
}

// HasErrors returns true if there are any errors.
func (e ValidationErrors) HasErrors() bool {
	return len(e) > 0
}

// Add adds a new validation error.
func (e *ValidationErrors) Add(field, message string) {
	*e = append(*e, &ValidationError{Field: field, Message: message})
}

// AddError adds an existing error.
func (e *ValidationErrors) AddError(err *ValidationError) {
	if err != nil {
		*e = append(*e, err)
	}
}

// Validator validates configuration.
type Validator struct {
	errors ValidationErrors
}

// NewValidator creates a new Validator.
func NewValidator() *Validator {
	return &Validator{}
}

// Errors returns all validation errors.
func (v *Validator) Errors() ValidationErrors {
	return v.errors
}

// HasErrors returns true if there are validation errors.
func (v *Validator) HasErrors() bool {
	return v.errors.HasErrors()
}

// Reset clears all validation errors.
func (v *Validator) Reset() {
	v.errors = nil
}

// AddError adds a validation error.
func (v *Validator) AddError(field, message string) {
	v.errors.Add(field, message)
}

// Required validates that a field is not empty.
func (v *Validator) Required(field string, value interface{}) bool {
	if isEmpty(value) {
		v.AddError(field, "is required")
		return false
	}
	return true
}

// MinLength validates minimum string length.
func (v *Validator) MinLength(field, value string, min int) bool {
	if len(value) < min {
		v.AddError(field, fmt.Sprintf("must be at least %d characters", min))
		return false
	}
	return true
}

// MaxLength validates maximum string length.
func (v *Validator) MaxLength(field, value string, max int) bool {
	if len(value) > max {
		v.AddError(field, fmt.Sprintf("must be at most %d characters", max))
		return false
	}
	return true
}

// MinValue validates minimum numeric value.
func (v *Validator) MinValue(field string, value, min int) bool {
	if value < min {
		v.AddError(field, fmt.Sprintf("must be at least %d", min))
		return false
	}
	return true
}

// MaxValue validates maximum numeric value.
func (v *Validator) MaxValue(field string, value, max int) bool {
	if value > max {
		v.AddError(field, fmt.Sprintf("must be at most %d", max))
		return false
	}
	return true
}

// Range validates that a value is within a range.
func (v *Validator) Range(field string, value, min, max int) bool {
	if value < min || value > max {
		v.AddError(field, fmt.Sprintf("must be between %d and %d", min, max))
		return false
	}
	return true
}

// Pattern validates against a regex pattern.
func (v *Validator) Pattern(field, value, pattern, description string) bool {
	re, err := regexp.Compile(pattern)
	if err != nil {
		v.AddError(field, fmt.Sprintf("invalid pattern: %s", err))
		return false
	}

	if !re.MatchString(value) {
		v.AddError(field, fmt.Sprintf("must match pattern: %s", description))
		return false
	}
	return true
}

// OneOf validates that value is one of the allowed values.
func (v *Validator) OneOf(field string, value interface{}, allowed ...interface{}) bool {
	for _, a := range allowed {
		if value == a {
			return true
		}
	}

	var allowedStr []string
	for _, a := range allowed {
		allowedStr = append(allowedStr, fmt.Sprintf("%v", a))
	}

	v.AddError(field, fmt.Sprintf("must be one of: %s", strings.Join(allowedStr, ", ")))
	return false
}

// ValidHost validates a hostname or IP address.
func (v *Validator) ValidHost(field, value string) bool {
	if value == "" {
		return true
	}

	// Check if it's a valid IP
	if ip := net.ParseIP(value); ip != nil {
		return true
	}

	// Check if it's a valid hostname
	if isValidHostname(value) {
		return true
	}

	v.AddError(field, "must be a valid hostname or IP address")
	return false
}

// ValidPort validates a port number.
func (v *Validator) ValidPort(field string, port int) bool {
	if port < 1 || port > 65535 {
		v.AddError(field, "must be a valid port (1-65535)")
		return false
	}
	return true
}

// ValidHostPort validates a host:port string.
func (v *Validator) ValidHostPort(field, value string) bool {
	if value == "" {
		return true
	}

	host, portStr, err := net.SplitHostPort(value)
	if err != nil {
		v.AddError(field, "must be in format host:port")
		return false
	}

	if !v.ValidHost(field+".host", host) {
		return false
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		v.AddError(field+".port", "must be a number")
		return false
	}

	return v.ValidPort(field+".port", port)
}

// ValidURL validates a URL.
func (v *Validator) ValidURL(field, value string) bool {
	if value == "" {
		return true
	}

	u, err := url.Parse(value)
	if err != nil {
		v.AddError(field, "must be a valid URL")
		return false
	}

	if u.Scheme == "" {
		v.AddError(field, "must have a scheme (http, https, etc.)")
		return false
	}

	return true
}

// ValidPath validates a file path exists.
func (v *Validator) ValidPath(field, path string, mustExist bool) bool {
	if path == "" {
		return true
	}

	// Check if path is absolute
	if !filepath.IsAbs(path) {
		v.AddError(field, "must be an absolute path")
		return false
	}

	if mustExist {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			v.AddError(field, "path does not exist")
			return false
		}
	}

	return true
}

// ValidDirectory validates a directory path.
func (v *Validator) ValidDirectory(field, path string, mustExist bool) bool {
	if path == "" {
		return true
	}

	if mustExist {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			v.AddError(field, "directory does not exist")
			return false
		}
		if !info.IsDir() {
			v.AddError(field, "path is not a directory")
			return false
		}
	}

	return true
}

// ValidFile validates a file path.
func (v *Validator) ValidFile(field, path string, mustExist bool) bool {
	if path == "" {
		return true
	}

	if mustExist {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			v.AddError(field, "file does not exist")
			return false
		}
		if info.IsDir() {
			v.AddError(field, "path is a directory, not a file")
			return false
		}
	}

	return true
}

// ValidDuration validates a duration string.
func (v *Validator) ValidDuration(field, value string) bool {
	if value == "" {
		return true
	}

	_, err := time.ParseDuration(value)
	if err != nil {
		v.AddError(field, "must be a valid duration (e.g., 30s, 5m, 1h)")
		return false
	}

	return true
}

// ValidDurationRange validates a duration is within a range.
func (v *Validator) ValidDurationRange(field string, d time.Duration, min, max time.Duration) bool {
	if d < min {
		v.AddError(field, fmt.Sprintf("must be at least %s", min))
		return false
	}
	if max > 0 && d > max {
		v.AddError(field, fmt.Sprintf("must be at most %s", max))
		return false
	}
	return true
}

// ValidIdentifier validates a PostgreSQL identifier.
func (v *Validator) ValidIdentifier(field, value string) bool {
	if value == "" {
		return true
	}

	// PostgreSQL identifiers: start with letter/underscore, contain letters/digits/underscores
	re := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	if !re.MatchString(value) {
		v.AddError(field, "must be a valid PostgreSQL identifier")
		return false
	}

	// Check length (max 63 characters for unquoted)
	if len(value) > 63 {
		v.AddError(field, "identifier too long (max 63 characters)")
		return false
	}

	// Check for reserved words
	reserved := []string{"all", "analyse", "analyze", "and", "any", "array", "as", "asc",
		"asymmetric", "both", "case", "cast", "check", "collate", "column", "constraint",
		"create", "current_catalog", "current_date", "current_role", "current_time",
		"current_timestamp", "current_user", "default", "deferrable", "desc", "distinct",
		"do", "else", "end", "except", "false", "fetch", "for", "foreign", "from", "grant",
		"group", "having", "in", "initially", "intersect", "into", "lateral", "leading",
		"limit", "localtime", "localtimestamp", "not", "null", "offset", "on", "only", "or",
		"order", "placing", "primary", "references", "returning", "select", "session_user",
		"some", "symmetric", "table", "then", "to", "trailing", "true", "union", "unique",
		"user", "using", "variadic", "when", "where", "window", "with"}

	lower := strings.ToLower(value)
	for _, r := range reserved {
		if lower == r {
			v.AddError(field, fmt.Sprintf("'%s' is a reserved word", value))
			return false
		}
	}

	return true
}

// ValidConnectionString validates a PostgreSQL connection string.
func (v *Validator) ValidConnectionString(field, value string) bool {
	if value == "" {
		return true
	}

	// Basic validation - check for required components
	// Connection string can be key=value or URI format
	if strings.HasPrefix(value, "postgres://") || strings.HasPrefix(value, "postgresql://") {
		if !v.ValidURL(field, value) {
			return false
		}
	}

	return true
}

// isEmpty checks if a value is empty.
func isEmpty(value interface{}) bool {
	if value == nil {
		return true
	}

	switch v := value.(type) {
	case string:
		return v == ""
	case int:
		return v == 0
	case int64:
		return v == 0
	case float64:
		return v == 0
	case bool:
		return false // bool is never "empty"
	case []string:
		return len(v) == 0
	case map[string]interface{}:
		return len(v) == 0
	default:
		return false
	}
}

// isValidHostname checks if a string is a valid hostname.
func isValidHostname(hostname string) bool {
	if len(hostname) > 253 {
		return false
	}

	// Split by dots and validate each label
	labels := strings.Split(hostname, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}

		// Labels must start and end with alphanumeric
		if !isAlphanumeric(label[0]) || !isAlphanumeric(label[len(label)-1]) {
			return false
		}

		// Labels can contain alphanumeric and hyphens
		for _, c := range label {
			if !isAlphanumeric(byte(c)) && c != '-' {
				return false
			}
		}
	}

	return true
}

// isAlphanumeric checks if a character is alphanumeric.
func isAlphanumeric(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// ConfigValidator validates Patroni configuration.
type ConfigValidator struct {
	*Validator
}

// NewConfigValidator creates a new ConfigValidator.
func NewConfigValidator() *ConfigValidator {
	return &ConfigValidator{
		Validator: NewValidator(),
	}
}

// ValidateDCS validates DCS configuration.
func (v *ConfigValidator) ValidateDCS(config map[string]interface{}) {
	// Check for at least one DCS type
	dcsTypes := []string{"etcd", "etcd3", "consul", "zookeeper", "kubernetes", "raft"}
	found := false
	for _, t := range dcsTypes {
		if _, ok := config[t]; ok {
			found = true
			break
		}
	}

	if !found {
		v.AddError("dcs", "at least one DCS backend must be configured")
		return
	}

	// Validate each DCS type
	if etcd, ok := config["etcd"].(map[string]interface{}); ok {
		v.validateEtcdConfig(etcd)
	}
	if etcd3, ok := config["etcd3"].(map[string]interface{}); ok {
		v.validateEtcdConfig(etcd3)
	}
	if consul, ok := config["consul"].(map[string]interface{}); ok {
		v.validateConsulConfig(consul)
	}
	if zk, ok := config["zookeeper"].(map[string]interface{}); ok {
		v.validateZookeeperConfig(zk)
	}
	if k8s, ok := config["kubernetes"].(map[string]interface{}); ok {
		v.validateKubernetesConfig(k8s)
	}
}

// validateEtcdConfig validates etcd/etcd3 configuration.
func (v *ConfigValidator) validateEtcdConfig(config map[string]interface{}) {
	// Validate hosts
	if hosts, ok := config["hosts"].([]interface{}); ok {
		for i, h := range hosts {
			if host, ok := h.(string); ok {
				v.ValidHostPort(fmt.Sprintf("etcd.hosts[%d]", i), host)
			}
		}
	} else if host, ok := config["host"].(string); ok {
		v.ValidHost("etcd.host", host)
	}

	// Validate TLS settings
	if cacert, ok := config["cacert"].(string); ok {
		v.ValidFile("etcd.cacert", cacert, true)
	}
	if cert, ok := config["cert"].(string); ok {
		v.ValidFile("etcd.cert", cert, true)
	}
	if key, ok := config["key"].(string); ok {
		v.ValidFile("etcd.key", key, true)
	}
}

// validateConsulConfig validates consul configuration.
func (v *ConfigValidator) validateConsulConfig(config map[string]interface{}) {
	if host, ok := config["host"].(string); ok {
		v.ValidHostPort("consul.host", host)
	}
}

// validateZookeeperConfig validates zookeeper configuration.
func (v *ConfigValidator) validateZookeeperConfig(config map[string]interface{}) {
	if hosts, ok := config["hosts"].([]interface{}); ok {
		for i, h := range hosts {
			if host, ok := h.(string); ok {
				v.ValidHostPort(fmt.Sprintf("zookeeper.hosts[%d]", i), host)
			}
		}
	}
}

// validateKubernetesConfig validates kubernetes configuration.
func (v *ConfigValidator) validateKubernetesConfig(config map[string]interface{}) {
	// Kubernetes config is typically auto-detected
	if namespace, ok := config["namespace"].(string); ok {
		v.ValidIdentifier("kubernetes.namespace", namespace)
	}
}

// ValidatePostgreSQL validates PostgreSQL configuration.
func (v *ConfigValidator) ValidatePostgreSQL(config map[string]interface{}) {
	// Data directory is required
	if dataDir, ok := config["data_dir"].(string); ok {
		v.Required("postgresql.data_dir", dataDir)
		v.ValidPath("postgresql.data_dir", dataDir, false)
	} else {
		v.AddError("postgresql.data_dir", "is required")
	}

	// Validate listen address
	if listen, ok := config["listen"].(string); ok {
		v.ValidHostPort("postgresql.listen", listen)
	}

	// Validate bin_dir if specified
	if binDir, ok := config["bin_dir"].(string); ok {
		v.ValidDirectory("postgresql.bin_dir", binDir, true)
	}

	// Validate authentication
	if auth, ok := config["authentication"].(map[string]interface{}); ok {
		v.validateAuthentication(auth)
	}

	// Validate parameters
	if params, ok := config["parameters"].(map[string]interface{}); ok {
		v.validatePostgreSQLParameters(params)
	}
}

// validateAuthentication validates authentication configuration.
func (v *ConfigValidator) validateAuthentication(config map[string]interface{}) {
	if replication, ok := config["replication"].(map[string]interface{}); ok {
		v.Required("postgresql.authentication.replication.username", replication["username"])
	}

	if superuser, ok := config["superuser"].(map[string]interface{}); ok {
		v.Required("postgresql.authentication.superuser.username", superuser["username"])
	}
}

// validatePostgreSQLParameters validates PostgreSQL parameters.
func (v *ConfigValidator) validatePostgreSQLParameters(params map[string]interface{}) {
	// Validate common parameters
	if maxConn, ok := params["max_connections"]; ok {
		if val, err := toInt(maxConn); err == nil {
			v.Range("postgresql.parameters.max_connections", val, 1, 262143)
		}
	}

	if walLevel, ok := params["wal_level"].(string); ok {
		v.OneOf("postgresql.parameters.wal_level", walLevel, "minimal", "replica", "logical")
	}

	if maxWalSenders, ok := params["max_wal_senders"]; ok {
		if val, err := toInt(maxWalSenders); err == nil {
			v.Range("postgresql.parameters.max_wal_senders", val, 0, 262143)
		}
	}
}

// ValidateBootstrap validates bootstrap configuration.
func (v *ConfigValidator) ValidateBootstrap(config map[string]interface{}) {
	// DCS settings
	if dcs, ok := config["dcs"].(map[string]interface{}); ok {
		// Validate TTL
		if ttl, ok := dcs["ttl"]; ok {
			if val, err := toInt(ttl); err == nil {
				v.MinValue("bootstrap.dcs.ttl", val, 10)
			}
		}

		// Validate loop_wait
		if loopWait, ok := dcs["loop_wait"]; ok {
			if val, err := toInt(loopWait); err == nil {
				v.Range("bootstrap.dcs.loop_wait", val, 1, 300)
			}
		}

		// Validate retry_timeout
		if retryTimeout, ok := dcs["retry_timeout"]; ok {
			if val, err := toInt(retryTimeout); err == nil {
				v.MinValue("bootstrap.dcs.retry_timeout", val, 1)
			}
		}
	}

	// Method
	if method, ok := config["method"].(string); ok {
		// Validate custom bootstrap method exists
		if method != "initdb" && method != "pg_basebackup" {
			// Check if method is defined
			if _, exists := config[method]; !exists {
				v.AddError("bootstrap.method", fmt.Sprintf("method '%s' is not defined", method))
			}
		}
	}
}

// ValidateRestAPI validates REST API configuration.
func (v *ConfigValidator) ValidateRestAPI(config map[string]interface{}) {
	if listen, ok := config["listen"].(string); ok {
		v.ValidHostPort("restapi.listen", listen)
	}

	if connectAddress, ok := config["connect_address"].(string); ok {
		v.ValidHostPort("restapi.connect_address", connectAddress)
	}

	// Validate TLS
	if certfile, ok := config["certfile"].(string); ok {
		v.ValidFile("restapi.certfile", certfile, true)
	}
	if keyfile, ok := config["keyfile"].(string); ok {
		v.ValidFile("restapi.keyfile", keyfile, true)
	}
}

// toInt converts a value to int.
func toInt(value interface{}) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case string:
		return strconv.Atoi(v)
	default:
		return 0, fmt.Errorf("cannot convert %T to int", value)
	}
}

// ValidateConfig validates the entire configuration.
func ValidateConfig(config map[string]interface{}) error {
	v := NewConfigValidator()

	// Validate required fields
	v.Required("scope", config["scope"])
	v.Required("name", config["name"])

	// Validate scope format
	if scope, ok := config["scope"].(string); ok {
		v.Pattern("scope", scope, `^[a-z][a-z0-9_-]*$`, "lowercase letters, numbers, underscores, hyphens")
	}

	// Validate name format
	if name, ok := config["name"].(string); ok {
		v.Pattern("name", name, `^[a-z][a-z0-9_-]*$`, "lowercase letters, numbers, underscores, hyphens")
	}

	// Validate DCS
	v.ValidateDCS(config)

	// Validate PostgreSQL
	if pg, ok := config["postgresql"].(map[string]interface{}); ok {
		v.ValidatePostgreSQL(pg)
	} else {
		v.AddError("postgresql", "is required")
	}

	// Validate bootstrap
	if bootstrap, ok := config["bootstrap"].(map[string]interface{}); ok {
		v.ValidateBootstrap(bootstrap)
	}

	// Validate REST API
	if restapi, ok := config["restapi"].(map[string]interface{}); ok {
		v.ValidateRestAPI(restapi)
	}

	if v.HasErrors() {
		return v.Errors()
	}

	return nil
}
