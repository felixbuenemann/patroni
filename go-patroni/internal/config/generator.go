// Package config provides configuration management for Patroni.
// This file contains the configuration generator for creating Patroni config files.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// NoValueMsg is used as a placeholder for values that need to be filled in.
	NoValueMsg = "#FIXME"
)

// AuthParameters maps libpq connection parameters to environment variables.
var AuthParameters = map[string]string{
	"user":           "PGUSER",
	"password":       "PGPASSWORD",
	"sslmode":        "PGSSLMODE",
	"sslcert":        "PGSSLCERT",
	"sslkey":         "PGSSLKEY",
	"sslrootcert":    "PGSSLROOTCERT",
	"sslcrl":         "PGSSLCRL",
	"gssencmode":     "PGGSSENCMODE",
	"channel_binding": "PGCHANNELBINDING",
}

// GetAddress tries to get hostname and IP address.
func GetAddress() (hostname, ip string) {
	hostname, err := os.Hostname()
	if err != nil {
		return NoValueMsg, NoValueMsg
	}

	addrs, err := net.LookupHost(hostname)
	if err != nil || len(addrs) == 0 {
		return hostname, NoValueMsg
	}

	// Prefer IPv4
	for _, addr := range addrs {
		if net.ParseIP(addr).To4() != nil {
			return hostname, addr
		}
	}

	return hostname, addrs[0]
}

// GeneratorConfig holds the generated configuration.
type GeneratorConfig struct {
	Scope     string                 `yaml:"scope"`
	Namespace string                 `yaml:"namespace,omitempty"`
	Name      string                 `yaml:"name"`
	RestAPI   map[string]interface{} `yaml:"restapi"`
	Bootstrap map[string]interface{} `yaml:"bootstrap,omitempty"`
	PostgreSQL map[string]interface{} `yaml:"postgresql"`
	DCS       map[string]interface{} `yaml:",inline"`
}

// ConfigGenerator generates Patroni configuration files.
type ConfigGenerator struct {
	Config     *GeneratorConfig
	OutputFile string
	PGMajor    int
}

// NewConfigGenerator creates a new configuration generator.
func NewConfigGenerator(outputFile string) *ConfigGenerator {
	hostname, ip := GetAddress()

	return &ConfigGenerator{
		OutputFile: outputFile,
		Config: &GeneratorConfig{
			Scope:     NoValueMsg,
			Namespace: "/patroni",
			Name:      hostname,
			RestAPI: map[string]interface{}{
				"listen":         fmt.Sprintf("%s:8008", ip),
				"connect_address": fmt.Sprintf("%s:8008", ip),
			},
			PostgreSQL: map[string]interface{}{
				"listen":         fmt.Sprintf("%s:5432", ip),
				"connect_address": fmt.Sprintf("%s:5432", ip),
				"data_dir":       NoValueMsg,
				"bin_dir":        NoValueMsg,
				"authentication": map[string]interface{}{
					"replication": map[string]interface{}{
						"username": "replicator",
						"password": NoValueMsg,
					},
					"superuser": map[string]interface{}{
						"username": "postgres",
						"password": NoValueMsg,
					},
				},
			},
		},
	}
}

// SetDCS sets the DCS configuration.
func (g *ConfigGenerator) SetDCS(dcsType string, config map[string]interface{}) {
	g.Config.DCS = map[string]interface{}{
		dcsType: config,
	}
}

// SetEtcd configures etcd as the DCS.
func (g *ConfigGenerator) SetEtcd(hosts []string) {
	g.SetDCS("etcd3", map[string]interface{}{
		"hosts": hosts,
	})
}

// SetConsul configures Consul as the DCS.
func (g *ConfigGenerator) SetConsul(host string) {
	g.SetDCS("consul", map[string]interface{}{
		"host": host,
	})
}

// SetZooKeeper configures ZooKeeper as the DCS.
func (g *ConfigGenerator) SetZooKeeper(hosts []string) {
	g.SetDCS("zookeeper", map[string]interface{}{
		"hosts": hosts,
	})
}

// SetBootstrap sets bootstrap configuration.
func (g *ConfigGenerator) SetBootstrap(dcs map[string]interface{}, initdb []map[string]interface{}) {
	g.Config.Bootstrap = map[string]interface{}{
		"dcs": dcs,
	}
	if len(initdb) > 0 {
		g.Config.Bootstrap["initdb"] = initdb
	}
}

// DetectPostgreSQLBinDir tries to detect the PostgreSQL bin directory.
func (g *ConfigGenerator) DetectPostgreSQLBinDir() string {
	// Common PostgreSQL installation paths
	paths := []string{
		"/usr/lib/postgresql/*/bin",
		"/usr/pgsql-*/bin",
		"/opt/postgresql/*/bin",
		"/usr/local/pgsql/bin",
		"/usr/bin",
	}

	for _, pattern := range paths {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, match := range matches {
			pgctl := filepath.Join(match, "pg_ctl")
			if _, err := os.Stat(pgctl); err == nil {
				return match
			}
		}
	}

	return NoValueMsg
}

// DetectDataDirectory tries to detect existing PostgreSQL data directory.
func (g *ConfigGenerator) DetectDataDirectory() string {
	// Check PGDATA environment variable
	if pgdata := os.Getenv("PGDATA"); pgdata != "" {
		return pgdata
	}

	// Common data directory paths
	paths := []string{
		"/var/lib/postgresql/*/main",
		"/var/lib/pgsql/*/data",
		"/var/lib/postgresql/data",
	}

	for _, pattern := range paths {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, match := range matches {
			pgVersion := filepath.Join(match, "PG_VERSION")
			if _, err := os.Stat(pgVersion); err == nil {
				return match
			}
		}
	}

	return NoValueMsg
}

// Generate generates the configuration and optionally writes to file.
func (g *ConfigGenerator) Generate() ([]byte, error) {
	// Try to detect PostgreSQL paths if not set
	if pg, ok := g.Config.PostgreSQL["bin_dir"].(string); ok && pg == NoValueMsg {
		g.Config.PostgreSQL["bin_dir"] = g.DetectPostgreSQLBinDir()
	}
	if pg, ok := g.Config.PostgreSQL["data_dir"].(string); ok && pg == NoValueMsg {
		g.Config.PostgreSQL["data_dir"] = g.DetectDataDirectory()
	}

	data, err := yaml.Marshal(g.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	if g.OutputFile != "" {
		if err := os.WriteFile(g.OutputFile, data, 0600); err != nil {
			return nil, fmt.Errorf("failed to write config file: %w", err)
		}
	}

	return data, nil
}

// GenerateFromRunningPostgreSQL generates config from a running PostgreSQL instance.
type RunningPostgreSQLGenerator struct {
	*ConfigGenerator
	DSN string
}

// NewRunningPostgreSQLGenerator creates a generator from a running PostgreSQL instance.
func NewRunningPostgreSQLGenerator(dsn, outputFile string) *RunningPostgreSQLGenerator {
	return &RunningPostgreSQLGenerator{
		ConfigGenerator: NewConfigGenerator(outputFile),
		DSN:            dsn,
	}
}

// ParseDSN parses a PostgreSQL connection string.
func ParseDSN(dsn string) map[string]string {
	params := make(map[string]string)

	// Handle both key=value and URI formats
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		// URI format - simplified parsing
		dsn = strings.TrimPrefix(dsn, "postgres://")
		dsn = strings.TrimPrefix(dsn, "postgresql://")

		// Split user:password@host:port/dbname
		if atIdx := strings.Index(dsn, "@"); atIdx != -1 {
			userPart := dsn[:atIdx]
			dsn = dsn[atIdx+1:]
			if colonIdx := strings.Index(userPart, ":"); colonIdx != -1 {
				params["user"] = userPart[:colonIdx]
				params["password"] = userPart[colonIdx+1:]
			} else {
				params["user"] = userPart
			}
		}

		if slashIdx := strings.Index(dsn, "/"); slashIdx != -1 {
			hostPart := dsn[:slashIdx]
			params["dbname"] = dsn[slashIdx+1:]
			if colonIdx := strings.Index(hostPart, ":"); colonIdx != -1 {
				params["host"] = hostPart[:colonIdx]
				params["port"] = hostPart[colonIdx+1:]
			} else {
				params["host"] = hostPart
			}
		}
	} else {
		// key=value format
		for _, part := range strings.Fields(dsn) {
			if eqIdx := strings.Index(part, "="); eqIdx != -1 {
				key := part[:eqIdx]
				value := part[eqIdx+1:]
				params[key] = value
			}
		}
	}

	return params
}
