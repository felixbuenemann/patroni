// Package config provides configuration management for Patroni.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/patroni/patroni-go/internal/dcs"
)

// Config represents the complete Patroni configuration.
type Config struct {
	mu sync.RWMutex

	// Core settings
	Scope     string `yaml:"scope" mapstructure:"scope"`
	Namespace string `yaml:"namespace" mapstructure:"namespace"`
	Name      string `yaml:"name" mapstructure:"name"`

	// Timing settings
	TTL          int `yaml:"ttl" mapstructure:"ttl"`
	LoopWait     int `yaml:"loop_wait" mapstructure:"loop_wait"`
	RetryTimeout int `yaml:"retry_timeout" mapstructure:"retry_timeout"`

	// Log settings
	Log LogConfig `yaml:"log" mapstructure:"log"`

	// REST API settings
	RestAPI RestAPIConfig `yaml:"restapi" mapstructure:"restapi"`

	// PostgreSQL settings
	PostgreSQL PostgreSQLConfig `yaml:"postgresql" mapstructure:"postgresql"`

	// Bootstrap settings
	Bootstrap BootstrapConfig `yaml:"bootstrap" mapstructure:"bootstrap"`

	// Watchdog settings
	Watchdog WatchdogConfig `yaml:"watchdog" mapstructure:"watchdog"`

	// Tags
	Tags map[string]interface{} `yaml:"tags" mapstructure:"tags"`

	// DCS-specific settings (only one should be configured)
	Etcd       *dcs.Config `yaml:"etcd" mapstructure:"etcd"`
	Etcd3      *dcs.Config `yaml:"etcd3" mapstructure:"etcd3"`
	Consul     *dcs.Config `yaml:"consul" mapstructure:"consul"`
	ZooKeeper  *dcs.Config `yaml:"zookeeper" mapstructure:"zookeeper"`
	Exhibitor  *dcs.Config `yaml:"exhibitor" mapstructure:"exhibitor"`
	Kubernetes *dcs.Config `yaml:"kubernetes" mapstructure:"kubernetes"`
	Raft       *dcs.Config `yaml:"raft" mapstructure:"raft"`
}

// LogConfig holds logging configuration.
type LogConfig struct {
	Level        string `yaml:"level" mapstructure:"level"`
	Format       string `yaml:"format" mapstructure:"format"`
	DateFormat   string `yaml:"dateformat" mapstructure:"dateformat"`
	MaxQueueSize int    `yaml:"max_queue_size" mapstructure:"max_queue_size"`
	Dir          string `yaml:"dir" mapstructure:"dir"`
	FileNum      int    `yaml:"file_num" mapstructure:"file_num"`
	FileSize     int    `yaml:"file_size" mapstructure:"file_size"`
}

// RestAPIConfig holds REST API configuration.
type RestAPIConfig struct {
	Listen             string            `yaml:"listen" mapstructure:"listen"`
	ConnectAddress     string            `yaml:"connect_address" mapstructure:"connect_address"`
	CertFile           string            `yaml:"certfile" mapstructure:"certfile"`
	KeyFile            string            `yaml:"keyfile" mapstructure:"keyfile"`
	CAFile             string            `yaml:"cafile" mapstructure:"cafile"`
	Username           string            `yaml:"username" mapstructure:"username"`
	Password           string            `yaml:"password" mapstructure:"password"`
	Verify             bool              `yaml:"verify_client" mapstructure:"verify_client"`
	Allowlist          []string          `yaml:"allowlist" mapstructure:"allowlist"`
	AllowlistInclude   []string          `yaml:"allowlist_include_members" mapstructure:"allowlist_include_members"`
	HTTPExtraHeaders   map[string]string `yaml:"http_extra_headers" mapstructure:"http_extra_headers"`
	HTTPSExtraHeaders  map[string]string `yaml:"https_extra_headers" mapstructure:"https_extra_headers"`
	RequestQueueSize   int               `yaml:"request_queue_size" mapstructure:"request_queue_size"`
}

// PostgreSQLConfig holds PostgreSQL configuration.
type PostgreSQLConfig struct {
	Listen             string                 `yaml:"listen" mapstructure:"listen"`
	ConnectAddress     string                 `yaml:"connect_address" mapstructure:"connect_address"`
	DataDir            string                 `yaml:"data_dir" mapstructure:"data_dir"`
	BinDir             string                 `yaml:"bin_dir" mapstructure:"bin_dir"`
	ConfigDir          string                 `yaml:"config_dir" mapstructure:"config_dir"`
	PgCtlTimeout       int                    `yaml:"pg_ctl_timeout" mapstructure:"pg_ctl_timeout"`
	UseSlots           bool                   `yaml:"use_slots" mapstructure:"use_slots"`
	UsePgRewind        bool                   `yaml:"use_pg_rewind" mapstructure:"use_pg_rewind"`
	Parameters         map[string]interface{} `yaml:"parameters" mapstructure:"parameters"`
	PgHBA              []string               `yaml:"pg_hba" mapstructure:"pg_hba"`
	PgIdent            []string               `yaml:"pg_ident" mapstructure:"pg_ident"`
	Authentication     AuthConfig             `yaml:"authentication" mapstructure:"authentication"`
	Callbacks          CallbacksConfig        `yaml:"callbacks" mapstructure:"callbacks"`
	CreateReplicaMethods []string             `yaml:"create_replica_methods" mapstructure:"create_replica_methods"`
	RecoveryConf       map[string]string      `yaml:"recovery_conf" mapstructure:"recovery_conf"`
	CustomConf         string                 `yaml:"custom_conf" mapstructure:"custom_conf"`
	BinName            map[string]string      `yaml:"bin_name" mapstructure:"bin_name"`
	PrePromote         string                 `yaml:"pre_promote" mapstructure:"pre_promote"`
	BeforeStop         string                 `yaml:"before_stop" mapstructure:"before_stop"`
}

// AuthConfig holds PostgreSQL authentication settings.
type AuthConfig struct {
	Superuser   UserAuth `yaml:"superuser" mapstructure:"superuser"`
	Replication UserAuth `yaml:"replication" mapstructure:"replication"`
	Rewind      UserAuth `yaml:"rewind" mapstructure:"rewind"`
}

// UserAuth holds credentials for a PostgreSQL user.
type UserAuth struct {
	Username string `yaml:"username" mapstructure:"username"`
	Password string `yaml:"password" mapstructure:"password"`
	SSLMode  string `yaml:"sslmode" mapstructure:"sslmode"`
	SSLCert  string `yaml:"sslcert" mapstructure:"sslcert"`
	SSLKey   string `yaml:"sslkey" mapstructure:"sslkey"`
	SSLRoot  string `yaml:"sslrootcert" mapstructure:"sslrootcert"`
	Channel  string `yaml:"channel_binding" mapstructure:"channel_binding"`
}

// CallbacksConfig holds callback script paths.
type CallbacksConfig struct {
	OnStart      string `yaml:"on_start" mapstructure:"on_start"`
	OnStop       string `yaml:"on_stop" mapstructure:"on_stop"`
	OnRestart    string `yaml:"on_restart" mapstructure:"on_restart"`
	OnReload     string `yaml:"on_reload" mapstructure:"on_reload"`
	OnRoleChange string `yaml:"on_role_change" mapstructure:"on_role_change"`
}

// BootstrapConfig holds bootstrap configuration.
type BootstrapConfig struct {
	Method          string                 `yaml:"method" mapstructure:"method"`
	DCS             map[string]interface{} `yaml:"dcs" mapstructure:"dcs"`
	InitDB          []interface{}          `yaml:"initdb" mapstructure:"initdb"`
	PgHBA           []string               `yaml:"pg_hba" mapstructure:"pg_hba"`
	Users           map[string]interface{} `yaml:"users" mapstructure:"users"`
	PostBootstrap   string                 `yaml:"post_bootstrap" mapstructure:"post_bootstrap"`
	PostInit        string                 `yaml:"post_init" mapstructure:"post_init"`
}

// WatchdogConfig holds watchdog configuration.
type WatchdogConfig struct {
	Mode   string `yaml:"mode" mapstructure:"mode"`
	Device string `yaml:"device" mapstructure:"device"`
	Safety int    `yaml:"safety_margin" mapstructure:"safety_margin"`
}

// Load loads configuration from a file.
func Load(configFile string) (*Config, error) {
	v := viper.New()

	// Set defaults
	setDefaults(v)

	// Handle config file
	if configFile != "" {
		v.SetConfigFile(configFile)
	} else {
		v.SetConfigName("patroni")
		v.SetConfigType("yaml")
		v.AddConfigPath("/etc/patroni")
		v.AddConfigPath("$HOME/.config/patroni")
		v.AddConfigPath(".")
	}

	// Read configuration file
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		log.Warn().Msg("No configuration file found, using defaults and environment variables")
	}

	// Environment variables override
	v.SetEnvPrefix("PATRONI")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()

	// Also check specific environment variables
	bindEnvVariables(v)

	config := &Config{}
	if err := v.Unmarshal(config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return config, nil
}

// setDefaults sets default configuration values.
func setDefaults(v *viper.Viper) {
	v.SetDefault("ttl", 30)
	v.SetDefault("loop_wait", 10)
	v.SetDefault("retry_timeout", 10)
	v.SetDefault("namespace", "/service/")

	v.SetDefault("postgresql.use_slots", true)
	v.SetDefault("postgresql.pg_ctl_timeout", 60)
	v.SetDefault("postgresql.data_dir", "/var/lib/postgresql/data")

	v.SetDefault("restapi.listen", "0.0.0.0:8008")

	v.SetDefault("watchdog.mode", "automatic")
	v.SetDefault("watchdog.device", "/dev/watchdog")

	v.SetDefault("log.level", "INFO")
	v.SetDefault("log.format", "text")
}

// bindEnvVariables binds specific environment variables.
func bindEnvVariables(v *viper.Viper) {
	envMappings := map[string]string{
		"scope":                       "PATRONI_SCOPE",
		"name":                        "PATRONI_NAME",
		"namespace":                   "PATRONI_NAMESPACE",
		"ttl":                         "PATRONI_TTL",
		"loop_wait":                   "PATRONI_LOOP_WAIT",
		"retry_timeout":               "PATRONI_RETRY_TIMEOUT",
		"restapi.listen":              "PATRONI_RESTAPI_LISTEN",
		"restapi.connect_address":     "PATRONI_RESTAPI_CONNECT_ADDRESS",
		"postgresql.listen":           "PATRONI_POSTGRESQL_LISTEN",
		"postgresql.connect_address":  "PATRONI_POSTGRESQL_CONNECT_ADDRESS",
		"postgresql.data_dir":         "PATRONI_POSTGRESQL_DATA_DIR",
		"postgresql.bin_dir":          "PATRONI_POSTGRESQL_BIN_DIR",
	}

	for key, env := range envMappings {
		if val := os.Getenv(env); val != "" {
			v.Set(key, val)
		}
	}
}

// Validate validates the configuration.
func (c *Config) Validate() error {
	if c.Scope == "" {
		return fmt.Errorf("scope is required")
	}
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if c.PostgreSQL.DataDir == "" {
		return fmt.Errorf("postgresql.data_dir is required")
	}

	// Ensure exactly one DCS is configured
	dcsCount := 0
	if c.Etcd != nil {
		dcsCount++
	}
	if c.Etcd3 != nil {
		dcsCount++
	}
	if c.Consul != nil {
		dcsCount++
	}
	if c.ZooKeeper != nil {
		dcsCount++
	}
	if c.Exhibitor != nil {
		dcsCount++
	}
	if c.Kubernetes != nil {
		dcsCount++
	}
	if c.Raft != nil {
		dcsCount++
	}

	if dcsCount == 0 {
		return fmt.Errorf("at least one DCS must be configured (etcd, etcd3, consul, zookeeper, kubernetes, or raft)")
	}

	if dcsCount > 1 {
		log.Warn().Msg("Multiple DCS backends configured, using the first one found")
	}

	return nil
}

// GetDCSType returns the configured DCS type and its configuration.
func (c *Config) GetDCSType() (string, *dcs.Config) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var dcsType string
	var dcsConfig *dcs.Config

	switch {
	case c.Etcd3 != nil:
		dcsType = "etcd3"
		dcsConfig = c.Etcd3
	case c.Etcd != nil:
		dcsType = "etcd"
		dcsConfig = c.Etcd
	case c.Consul != nil:
		dcsType = "consul"
		dcsConfig = c.Consul
	case c.ZooKeeper != nil:
		dcsType = "zookeeper"
		dcsConfig = c.ZooKeeper
	case c.Exhibitor != nil:
		dcsType = "exhibitor"
		dcsConfig = c.Exhibitor
	case c.Kubernetes != nil:
		dcsType = "kubernetes"
		dcsConfig = c.Kubernetes
	case c.Raft != nil:
		dcsType = "raft"
		dcsConfig = c.Raft
	}

	// Populate DCS config with common settings
	if dcsConfig != nil {
		dcsConfig.Scope = c.Scope
		dcsConfig.Name = c.Name
		dcsConfig.Namespace = c.Namespace
		dcsConfig.TTL = c.TTL
		dcsConfig.LoopWait = c.LoopWait
		dcsConfig.RetryTimeout = c.RetryTimeout
	}

	return dcsType, dcsConfig
}

// GetAPIURL returns the REST API URL for this node.
func (c *Config) GetAPIURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	addr := c.RestAPI.ConnectAddress
	if addr == "" {
		addr = c.RestAPI.Listen
	}

	scheme := "http"
	if c.RestAPI.CertFile != "" {
		scheme = "https"
	}

	return fmt.Sprintf("%s://%s", scheme, addr)
}

// GetConnectionURL returns the PostgreSQL connection URL for this node.
func (c *Config) GetConnectionURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	addr := c.PostgreSQL.ConnectAddress
	if addr == "" {
		addr = c.PostgreSQL.Listen
	}

	host, port := parseAddress(addr)
	user := c.PostgreSQL.Authentication.Superuser.Username
	if user == "" {
		user = "postgres"
	}

	return fmt.Sprintf("postgres://%s@%s:%s/postgres", user, host, port)
}

// parseAddress parses host:port into separate components.
func parseAddress(addr string) (host, port string) {
	parts := strings.Split(addr, ":")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return addr, "5432"
}

// GetDataDir returns the PostgreSQL data directory.
func (c *Config) GetDataDir() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.PostgreSQL.DataDir
}

// SaveDynamicConfig saves dynamic configuration to a file.
func (c *Config) SaveDynamicConfig(dynamicConfig map[string]interface{}) error {
	c.mu.RLock()
	dataDir := c.PostgreSQL.DataDir
	c.mu.RUnlock()

	path := filepath.Join(dataDir, "patroni.dynamic.json")
	data, err := yaml.Marshal(dynamicConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal dynamic config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write dynamic config: %w", err)
	}

	return nil
}

// LoadDynamicConfig loads dynamic configuration from a file.
func (c *Config) LoadDynamicConfig() (map[string]interface{}, error) {
	c.mu.RLock()
	dataDir := c.PostgreSQL.DataDir
	c.mu.RUnlock()

	path := filepath.Join(dataDir, "patroni.dynamic.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read dynamic config: %w", err)
	}

	var config map[string]interface{}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse dynamic config: %w", err)
	}

	return config, nil
}

// ParseIntWithUnits parses an integer value with optional unit suffix.
func ParseIntWithUnits(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}

	// Check for unit suffix
	multiplier := int64(1)
	suffix := strings.ToUpper(value[len(value)-1:])
	numPart := value

	switch suffix {
	case "K":
		multiplier = 1024
		numPart = value[:len(value)-1]
	case "M":
		multiplier = 1024 * 1024
		numPart = value[:len(value)-1]
	case "G":
		multiplier = 1024 * 1024 * 1024
		numPart = value[:len(value)-1]
	case "T":
		multiplier = 1024 * 1024 * 1024 * 1024
		numPart = value[:len(value)-1]
	}

	// Also check for "B" suffix (e.g., "KB", "MB")
	if len(numPart) > 0 {
		lastChar := strings.ToUpper(numPart[len(numPart)-1:])
		if lastChar == "B" {
			numPart = numPart[:len(numPart)-1]
		}
	}

	num, err := strconv.ParseInt(strings.TrimSpace(numPart), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %s", value)
	}

	return num * multiplier, nil
}
