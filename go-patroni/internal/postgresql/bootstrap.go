package postgresql

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/patroni/patroni-go/pkg/types"
)

// Bootstrap initializes a new PostgreSQL cluster.
func (pg *Postgresql) Bootstrap(ctx context.Context) error {
	if pg.dataDirectoryExists() {
		return fmt.Errorf("data directory already exists and is not empty")
	}

	log.Info().Str("data_dir", pg.dataDir).Msg("Bootstrapping new PostgreSQL cluster")
	pg.setState(types.PostgresqlStateBootstrapStarting)

	// Run initdb
	if err := pg.initdb(ctx); err != nil {
		pg.setState(types.PostgresqlStateCrashed)
		return fmt.Errorf("initdb failed: %w", err)
	}

	// Get the major version
	pg.mu.Lock()
	pg.majorVersion = pg.getMajorVersion()
	pg.mu.Unlock()

	// Generate configuration
	if err := pg.generateConfiguration(); err != nil {
		return fmt.Errorf("failed to generate configuration: %w", err)
	}

	// Start PostgreSQL
	if err := pg.Start(ctx); err != nil {
		return fmt.Errorf("failed to start after bootstrap: %w", err)
	}

	// Run post-bootstrap steps
	if err := pg.postBootstrap(ctx); err != nil {
		log.Warn().Err(err).Msg("Post-bootstrap steps failed")
	}

	pg.setRole(types.PostgresqlRolePrimary)

	return nil
}

// initdb runs the initdb command to initialize a new database cluster.
func (pg *Postgresql) initdb(ctx context.Context) error {
	log.Info().Msg("Running initdb")

	args := []string{
		"-D", pg.dataDir,
	}

	// Add initdb options from configuration
	if pg.config.Bootstrap.InitDB != nil {
		for _, opt := range pg.config.Bootstrap.InitDB {
			switch v := opt.(type) {
			case string:
				args = append(args, v)
			case map[string]interface{}:
				for k, val := range v {
					switch vv := val.(type) {
					case string:
						args = append(args, fmt.Sprintf("--%s=%s", k, vv))
					case bool:
						if vv {
							args = append(args, fmt.Sprintf("--%s", k))
						}
					}
				}
			}
		}
	}

	// Set default encoding if not specified
	hasEncoding := false
	for _, arg := range args {
		if strings.HasPrefix(arg, "-E") || strings.HasPrefix(arg, "--encoding") {
			hasEncoding = true
			break
		}
	}
	if !hasEncoding {
		args = append(args, "-E", "UTF8")
	}

	// Set authentication method
	auth := pg.config.PostgreSQL.Authentication.Superuser
	if auth.Username != "" {
		args = append(args, "-U", auth.Username)
	}

	cmd := exec.CommandContext(ctx, pg.pgCommand("initdb"), args...)
	cmd.Env = append(os.Environ(), "PGDATA="+pg.dataDir)

	// Set password via environment if provided
	if auth.Password != "" {
		pwfile := filepath.Join(pg.dataDir, ".pgpass")
		if err := os.WriteFile(pwfile, []byte(auth.Password), 0600); err != nil {
			return fmt.Errorf("failed to write password file: %w", err)
		}
		defer os.Remove(pwfile)
		cmd.Env = append(cmd.Env, "PGPASSFILE="+pwfile)
		args = append(args, "--pwfile="+pwfile)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("initdb failed: %w, output: %s", err, string(output))
	}

	log.Info().Msg("initdb completed successfully")
	return nil
}

// generateConfiguration generates PostgreSQL configuration files.
func (pg *Postgresql) generateConfiguration() error {
	log.Info().Msg("Generating PostgreSQL configuration")

	// Write postgresql.conf
	if err := pg.writePostgresqlConf(); err != nil {
		return fmt.Errorf("failed to write postgresql.conf: %w", err)
	}

	// Write pg_hba.conf
	if err := pg.writePgHba(); err != nil {
		return fmt.Errorf("failed to write pg_hba.conf: %w", err)
	}

	return nil
}

// writePostgresqlConf writes the postgresql.conf file.
func (pg *Postgresql) writePostgresqlConf() error {
	confPath := filepath.Join(pg.dataDir, "postgresql.conf")

	// Include patroni.dynamic.conf
	includeStr := "include 'patroni.dynamic.conf'\n"

	// Build the main configuration
	var conf strings.Builder
	conf.WriteString("# Configuration managed by Patroni\n")

	// Listen address
	if pg.config.PostgreSQL.Listen != "" {
		host := pg.getHost()
		port := pg.getPort()
		if host == "*" || host == "0.0.0.0" {
			conf.WriteString(fmt.Sprintf("listen_addresses = '*'\n"))
		} else {
			conf.WriteString(fmt.Sprintf("listen_addresses = '%s'\n", host))
		}
		conf.WriteString(fmt.Sprintf("port = %s\n", port))
	}

	// WAL level for replication
	conf.WriteString("wal_level = replica\n")
	conf.WriteString("hot_standby = on\n")
	conf.WriteString("max_wal_senders = 10\n")
	conf.WriteString("max_replication_slots = 10\n")
	conf.WriteString("wal_log_hints = on\n")

	// Add user-defined parameters
	for key, value := range pg.config.PostgreSQL.Parameters {
		conf.WriteString(fmt.Sprintf("%s = '%v'\n", key, value))
	}

	conf.WriteString("\n" + includeStr)

	// Write the configuration file
	if err := os.WriteFile(confPath, []byte(conf.String()), 0600); err != nil {
		return fmt.Errorf("failed to write postgresql.conf: %w", err)
	}

	// Create empty patroni.dynamic.conf
	dynamicConfPath := filepath.Join(pg.dataDir, "patroni.dynamic.conf")
	if err := os.WriteFile(dynamicConfPath, []byte("# Dynamic configuration managed by Patroni\n"), 0600); err != nil {
		return fmt.Errorf("failed to write patroni.dynamic.conf: %w", err)
	}

	return nil
}

// writePgHba writes the pg_hba.conf file.
func (pg *Postgresql) writePgHba() error {
	hbaPath := filepath.Join(pg.dataDir, "pg_hba.conf")

	var hba strings.Builder
	hba.WriteString("# PostgreSQL Client Authentication Configuration File\n")
	hba.WriteString("# Managed by Patroni\n\n")

	// Local connections
	hba.WriteString("local   all             all                                     trust\n")
	hba.WriteString("host    all             all             127.0.0.1/32            trust\n")
	hba.WriteString("host    all             all             ::1/128                 trust\n")

	// Replication connections
	hba.WriteString("\n# Replication connections\n")
	hba.WriteString("local   replication     all                                     trust\n")
	hba.WriteString("host    replication     all             127.0.0.1/32            trust\n")
	hba.WriteString("host    replication     all             ::1/128                 trust\n")

	// Add configured HBA rules
	if pg.config.Bootstrap.PgHBA != nil {
		hba.WriteString("\n# Configured rules\n")
		for _, rule := range pg.config.Bootstrap.PgHBA {
			hba.WriteString(rule + "\n")
		}
	}

	if pg.config.PostgreSQL.PgHBA != nil {
		hba.WriteString("\n# Additional configured rules\n")
		for _, rule := range pg.config.PostgreSQL.PgHBA {
			hba.WriteString(rule + "\n")
		}
	}

	// Default allow all (for development/testing)
	hba.WriteString("\n# Default rules\n")
	hba.WriteString("host    all             all             0.0.0.0/0               md5\n")
	hba.WriteString("host    all             all             ::/0                    md5\n")
	hba.WriteString("host    replication     all             0.0.0.0/0               md5\n")
	hba.WriteString("host    replication     all             ::/0                    md5\n")

	if err := os.WriteFile(hbaPath, []byte(hba.String()), 0600); err != nil {
		return fmt.Errorf("failed to write pg_hba.conf: %w", err)
	}

	return nil
}

// postBootstrap runs post-bootstrap operations.
func (pg *Postgresql) postBootstrap(ctx context.Context) error {
	// Create users
	if err := pg.createUsers(ctx); err != nil {
		log.Warn().Err(err).Msg("Failed to create users")
	}

	// Run post_bootstrap script
	if pg.config.Bootstrap.PostBootstrap != "" {
		if err := pg.executeCallback(ctx, pg.config.Bootstrap.PostBootstrap, "post_bootstrap"); err != nil {
			return fmt.Errorf("post_bootstrap callback failed: %w", err)
		}
	}

	// Run post_init script
	if pg.config.Bootstrap.PostInit != "" {
		if err := pg.executeCallback(ctx, pg.config.Bootstrap.PostInit, "post_init"); err != nil {
			return fmt.Errorf("post_init callback failed: %w", err)
		}
	}

	return nil
}

// createUsers creates configured users.
func (pg *Postgresql) createUsers(ctx context.Context) error {
	// Create replication user
	repl := pg.config.PostgreSQL.Authentication.Replication
	if repl.Username != "" && repl.Username != "postgres" {
		query := fmt.Sprintf("CREATE USER %s REPLICATION", quoteIdent(repl.Username))
		if repl.Password != "" {
			query += fmt.Sprintf(" PASSWORD %s", quoteIdent(repl.Password))
		}

		if err := pg.Exec(ctx, query); err != nil {
			log.Warn().Err(err).Str("user", repl.Username).Msg("Failed to create replication user")
		}
	}

	// Create rewind user
	rewind := pg.config.PostgreSQL.Authentication.Rewind
	if rewind.Username != "" && rewind.Username != "postgres" && rewind.Username != repl.Username {
		query := fmt.Sprintf("CREATE USER %s", quoteIdent(rewind.Username))
		if rewind.Password != "" {
			query += fmt.Sprintf(" PASSWORD %s", quoteIdent(rewind.Password))
		}

		if err := pg.Exec(ctx, query); err != nil {
			log.Warn().Err(err).Str("user", rewind.Username).Msg("Failed to create rewind user")
		}
	}

	return nil
}

// Clone creates a new replica from an existing primary.
func (pg *Postgresql) Clone(ctx context.Context, primaryConnInfo string) error {
	if pg.dataDirectoryExists() {
		return fmt.Errorf("data directory already exists")
	}

	log.Info().Str("primary", primaryConnInfo).Msg("Cloning from primary")
	pg.setState(types.PostgresqlStateCreatingReplica)

	// Use pg_basebackup
	if err := pg.pgBasebackup(ctx, primaryConnInfo); err != nil {
		pg.setState(types.PostgresqlStateCrashed)
		return fmt.Errorf("pg_basebackup failed: %w", err)
	}

	// Get the major version
	pg.mu.Lock()
	pg.majorVersion = pg.getMajorVersion()
	pg.mu.Unlock()

	// Configure as replica
	if err := pg.configureReplica(primaryConnInfo); err != nil {
		return fmt.Errorf("failed to configure as replica: %w", err)
	}

	pg.setRole(types.PostgresqlRoleReplica)

	return nil
}

// pgBasebackup runs pg_basebackup to clone from a primary.
func (pg *Postgresql) pgBasebackup(ctx context.Context, primaryConnInfo string) error {
	log.Info().Msg("Running pg_basebackup")

	// Parse connection info
	parts := strings.Split(primaryConnInfo, " ")
	host := "localhost"
	port := "5432"
	user := "postgres"

	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			switch kv[0] {
			case "host":
				host = kv[1]
			case "port":
				port = kv[1]
			case "user":
				user = kv[1]
			}
		}
	}

	args := []string{
		"-D", pg.dataDir,
		"-h", host,
		"-p", port,
		"-U", user,
		"-X", "stream",
		"-P",
		"-R", // Create standby.signal and configure recovery
	}

	// Add slot option if available
	if pg.config.PostgreSQL.UseSlots {
		slotName := types.SlotNameFromMemberName(pg.name)
		args = append(args, "-S", slotName)
	}

	cmd := exec.CommandContext(ctx, pg.pgCommand("pg_basebackup"), args...)
	cmd.Env = os.Environ()

	// Set password via environment if provided
	repl := pg.config.PostgreSQL.Authentication.Replication
	if repl.Password != "" {
		cmd.Env = append(cmd.Env, "PGPASSWORD="+repl.Password)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pg_basebackup failed: %w, output: %s", err, string(output))
	}

	log.Info().Msg("pg_basebackup completed successfully")
	return nil
}
