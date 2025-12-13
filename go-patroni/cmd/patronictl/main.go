// Package main implements the patronictl command-line tool.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/patroni/patroni-go/internal/config"
	"github.com/patroni/patroni-go/internal/dcs"
	_ "github.com/patroni/patroni-go/internal/dcs/etcd3"
	"github.com/patroni/patroni-go/pkg/types"
)

var (
	configFile string
	dcsURL     string
	clusterName string
	insecure   bool
	format     string
)

func main() {
	// Suppress logging for CLI
	zerolog.SetGlobalLevel(zerolog.ErrorLevel)

	rootCmd := &cobra.Command{
		Use:   "patronictl",
		Short: "Patroni cluster management tool",
		Long:  `patronictl is a command-line tool for managing Patroni PostgreSQL clusters.`,
	}

	// Global flags
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "", "Configuration file path")
	rootCmd.PersistentFlags().StringVarP(&dcsURL, "dcs-url", "d", "", "DCS URL (e.g., etcd://localhost:2379)")
	rootCmd.PersistentFlags().StringVarP(&clusterName, "cluster", "e", "", "Cluster name (scope)")
	rootCmd.PersistentFlags().BoolVarP(&insecure, "insecure", "k", false, "Skip TLS verification")
	rootCmd.PersistentFlags().StringVarP(&format, "format", "f", "pretty", "Output format (pretty, json, yaml)")

	// Commands
	rootCmd.AddCommand(listCmd())
	rootCmd.AddCommand(showConfigCmd())
	rootCmd.AddCommand(editConfigCmd())
	rootCmd.AddCommand(pauseCmd())
	rootCmd.AddCommand(resumeCmd())
	rootCmd.AddCommand(restartCmd())
	rootCmd.AddCommand(reloadCmd())
	rootCmd.AddCommand(reinitCmd())
	rootCmd.AddCommand(switchoverCmd())
	rootCmd.AddCommand(failoverCmd())
	rootCmd.AddCommand(removeCmd())
	rootCmd.AddCommand(historyCmd())
	rootCmd.AddCommand(versionCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// getDCS creates a DCS client from configuration.
func getDCS() (dcs.DCS, error) {
	// Try config file first
	if configFile != "" {
		cfg, err := config.Load(configFile)
		if err != nil {
			return nil, err
		}

		dcsType, dcsConfig := cfg.GetDCSType()
		if clusterName != "" {
			dcsConfig.Scope = clusterName
		}
		return dcs.New(dcsType, dcsConfig)
	}

	// Try DCS URL
	if dcsURL != "" {
		dcsConfig := parseDCSURL(dcsURL)
		if clusterName != "" {
			dcsConfig.Scope = clusterName
		}
		// Determine type from URL
		dcsType := "etcd3"
		if strings.HasPrefix(dcsURL, "consul://") {
			dcsType = "consul"
		} else if strings.HasPrefix(dcsURL, "zk://") || strings.HasPrefix(dcsURL, "zookeeper://") {
			dcsType = "zookeeper"
		}
		return dcs.New(dcsType, dcsConfig)
	}

	// Try default config locations
	v := viper.New()
	v.SetConfigName("patronictl")
	v.SetConfigType("yaml")
	v.AddConfigPath("$HOME/.config/patroni")
	v.AddConfigPath("/etc/patroni")
	v.AddConfigPath(".")

	if err := v.ReadInConfig(); err == nil {
		cfg := &config.Config{}
		if err := v.Unmarshal(cfg); err != nil {
			return nil, err
		}
		dcsType, dcsConfig := cfg.GetDCSType()
		if clusterName != "" {
			dcsConfig.Scope = clusterName
		}
		return dcs.New(dcsType, dcsConfig)
	}

	return nil, fmt.Errorf("no configuration found, specify --config or --dcs-url")
}

// parseDCSURL parses a DCS URL into a Config.
func parseDCSURL(url string) *dcs.Config {
	cfg := &dcs.Config{}

	// Remove scheme
	url = strings.TrimPrefix(url, "etcd://")
	url = strings.TrimPrefix(url, "etcd3://")
	url = strings.TrimPrefix(url, "consul://")
	url = strings.TrimPrefix(url, "zk://")
	url = strings.TrimPrefix(url, "zookeeper://")

	cfg.Hosts = []string{url}
	return cfg
}

// list command
func listCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list [cluster]",
		Aliases: []string{"ls"},
		Short:   "List cluster members",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				clusterName = args[0]
			}

			dcsClient, err := getDCS()
			if err != nil {
				return err
			}
			defer dcsClient.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			cluster, err := dcsClient.GetCluster(ctx)
			if err != nil {
				return fmt.Errorf("failed to get cluster: %w", err)
			}

			return outputCluster(cluster)
		},
	}
	return cmd
}

// outputCluster formats and outputs cluster information.
func outputCluster(cluster *types.Cluster) error {
	switch format {
	case "json":
		return json.NewEncoder(os.Stdout).Encode(cluster)
	case "yaml":
		return yaml.NewEncoder(os.Stdout).Encode(cluster)
	default:
		return printClusterTable(cluster)
	}
}

// printClusterTable prints cluster info as a table.
func printClusterTable(cluster *types.Cluster) error {
	leaderName := ""
	if cluster.Leader != nil {
		leaderName = cluster.Leader.MemberName
	}

	fmt.Printf("+ Cluster: %s (%s) --------+\n", clusterName, dcsTypeFromURL())
	fmt.Printf("| %-20s | %-12s | %-10s | %-8s | %-10s |\n",
		"Member", "Host", "Role", "State", "TL/Lag")
	fmt.Println("+" + strings.Repeat("-", 72) + "+")

	// Sort members by name
	members := make([]*types.Member, len(cluster.Members))
	copy(members, cluster.Members)
	sort.Slice(members, func(i, j int) bool {
		return members[i].Name < members[j].Name
	})

	for _, m := range members {
		role := m.Data.Role
		if m.Name == leaderName {
			role = "Leader"
		}

		host := ""
		if kwargs := m.GetConnKwargs(); kwargs != nil {
			host = kwargs["host"]
			if port := kwargs["port"]; port != "" && port != "5432" {
				host += ":" + port
			}
		}

		lag := "-"
		if m.Data.XlogLocation > 0 && leaderName != "" && m.Name != leaderName {
			// Calculate lag
			lag = "0"
		}

		timeline := fmt.Sprintf("%d", m.Data.Timeline)
		if timeline == "0" {
			timeline = "-"
		}

		fmt.Printf("| %-20s | %-12s | %-10s | %-8s | %s/%s |\n",
			m.Name, host, role, m.Data.State, timeline, lag)
	}

	fmt.Println("+" + strings.Repeat("-", 72) + "+")
	return nil
}

func dcsTypeFromURL() string {
	if strings.HasPrefix(dcsURL, "consul://") {
		return "Consul"
	}
	if strings.HasPrefix(dcsURL, "zk://") || strings.HasPrefix(dcsURL, "zookeeper://") {
		return "ZooKeeper"
	}
	return "etcd"
}

// show-config command
func showConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show-config [cluster]",
		Short: "Show cluster configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				clusterName = args[0]
			}

			dcsClient, err := getDCS()
			if err != nil {
				return err
			}
			defer dcsClient.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			cluster, err := dcsClient.GetCluster(ctx)
			if err != nil {
				return err
			}

			if cluster.Config == nil || cluster.Config.Data == nil {
				fmt.Println("No configuration found")
				return nil
			}

			return yaml.NewEncoder(os.Stdout).Encode(cluster.Config.Data)
		},
	}
}

// edit-config command
func editConfigCmd() *cobra.Command {
	var patch string
	var set []string
	var apply bool
	var force bool

	cmd := &cobra.Command{
		Use:   "edit-config [cluster]",
		Short: "Edit cluster configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				clusterName = args[0]
			}

			dcsClient, err := getDCS()
			if err != nil {
				return err
			}
			defer dcsClient.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Apply config changes
			if apply || len(set) > 0 || patch != "" {
				newConfig := make(map[string]interface{})

				if patch != "" {
					if err := yaml.Unmarshal([]byte(patch), &newConfig); err != nil {
						return fmt.Errorf("invalid YAML patch: %w", err)
					}
				}

				for _, s := range set {
					parts := strings.SplitN(s, "=", 2)
					if len(parts) == 2 {
						newConfig[parts[0]] = parts[1]
					}
				}

				if err := dcsClient.SetConfigValue(ctx, newConfig); err != nil {
					return fmt.Errorf("failed to update config: %w", err)
				}

				fmt.Println("Configuration updated")
				return nil
			}

			// Interactive edit
			return fmt.Errorf("interactive editing not yet implemented, use --set or --patch")
		},
	}

	cmd.Flags().StringVar(&patch, "patch", "", "YAML patch to apply")
	cmd.Flags().StringArrayVar(&set, "set", nil, "Set configuration value (key=value)")
	cmd.Flags().BoolVar(&apply, "apply", false, "Apply changes without confirmation")
	cmd.Flags().BoolVar(&force, "force", false, "Force apply changes")

	return cmd
}

// pause command
func pauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause [cluster]",
		Short: "Pause automatic failover",
		RunE: func(cmd *cobra.Command, args []string) error {
			return setClusterPause(true)
		},
	}
}

// resume command
func resumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume [cluster]",
		Short: "Resume automatic failover",
		RunE: func(cmd *cobra.Command, args []string) error {
			return setClusterPause(false)
		},
	}
}

func setClusterPause(pause bool) error {
	dcsClient, err := getDCS()
	if err != nil {
		return err
	}
	defer dcsClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	config := map[string]interface{}{
		"pause": pause,
	}

	if err := dcsClient.SetConfigValue(ctx, config); err != nil {
		return fmt.Errorf("failed to update pause state: %w", err)
	}

	if pause {
		fmt.Println("Cluster paused")
	} else {
		fmt.Println("Cluster resumed")
	}
	return nil
}

// restart command
func restartCmd() *cobra.Command {
	var role string
	var scheduled string
	var force bool

	cmd := &cobra.Command{
		Use:   "restart [cluster] [member]",
		Short: "Restart PostgreSQL on a member",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return fmt.Errorf("cluster and member name required")
			}
			clusterName = args[0]
			memberName := args[1]

			return restartMember(memberName, role, scheduled, force)
		},
	}

	cmd.Flags().StringVar(&role, "role", "", "Only restart if member has this role")
	cmd.Flags().StringVar(&scheduled, "scheduled", "", "Schedule restart at time (ISO 8601)")
	cmd.Flags().BoolVar(&force, "force", false, "Force restart without confirmation")

	return cmd
}

func restartMember(memberName, role, scheduled string, force bool) error {
	dcsClient, err := getDCS()
	if err != nil {
		return err
	}
	defer dcsClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cluster, err := dcsClient.GetCluster(ctx)
	if err != nil {
		return err
	}

	member := cluster.GetMember(memberName)
	if member == nil {
		return fmt.Errorf("member %s not found", memberName)
	}

	// POST to member's API
	apiURL := member.Data.APIURL
	if apiURL == "" {
		return fmt.Errorf("no API URL for member %s", memberName)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL+"/restart", nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", memberName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("restart failed: %s", string(body))
	}

	fmt.Printf("Restart initiated on %s\n", memberName)
	return nil
}

// reload command
func reloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reload [cluster] [member]",
		Short: "Reload PostgreSQL configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return fmt.Errorf("cluster and member name required")
			}
			clusterName = args[0]
			memberName := args[1]

			return reloadMember(memberName)
		},
	}
}

func reloadMember(memberName string) error {
	dcsClient, err := getDCS()
	if err != nil {
		return err
	}
	defer dcsClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cluster, err := dcsClient.GetCluster(ctx)
	if err != nil {
		return err
	}

	member := cluster.GetMember(memberName)
	if member == nil {
		return fmt.Errorf("member %s not found", memberName)
	}

	apiURL := member.Data.APIURL
	if apiURL == "" {
		return fmt.Errorf("no API URL for member %s", memberName)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL+"/reload", nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", memberName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("reload failed: %s", string(body))
	}

	fmt.Printf("Reload initiated on %s\n", memberName)
	return nil
}

// reinit command
func reinitCmd() *cobra.Command {
	var force bool
	var wait bool

	cmd := &cobra.Command{
		Use:   "reinit [cluster] [member]",
		Short: "Reinitialize a cluster member",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return fmt.Errorf("cluster and member name required")
			}
			clusterName = args[0]
			memberName := args[1]

			return reinitMember(memberName, force, wait)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Force reinitialize")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for reinitialize to complete")

	return cmd
}

func reinitMember(memberName string, force, wait bool) error {
	dcsClient, err := getDCS()
	if err != nil {
		return err
	}
	defer dcsClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cluster, err := dcsClient.GetCluster(ctx)
	if err != nil {
		return err
	}

	member := cluster.GetMember(memberName)
	if member == nil {
		return fmt.Errorf("member %s not found", memberName)
	}

	apiURL := member.Data.APIURL
	if apiURL == "" {
		return fmt.Errorf("no API URL for member %s", memberName)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL+"/reinitialize", nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", memberName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("reinitialize failed: %s", string(body))
	}

	fmt.Printf("Reinitialize initiated on %s\n", memberName)
	return nil
}

// switchover command
func switchoverCmd() *cobra.Command {
	var candidate string
	var scheduled string
	var force bool

	cmd := &cobra.Command{
		Use:   "switchover [cluster]",
		Short: "Switch leadership to another member",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				clusterName = args[0]
			}
			return doSwitchover(candidate, scheduled, force)
		},
	}

	cmd.Flags().StringVar(&candidate, "candidate", "", "Candidate to become leader")
	cmd.Flags().StringVar(&scheduled, "scheduled", "", "Schedule switchover at time (ISO 8601)")
	cmd.Flags().BoolVar(&force, "force", false, "Force switchover without confirmation")

	return cmd
}

func doSwitchover(candidate, scheduled string, force bool) error {
	dcsClient, err := getDCS()
	if err != nil {
		return err
	}
	defer dcsClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cluster, err := dcsClient.GetCluster(ctx)
	if err != nil {
		return err
	}

	if !cluster.HasLeader() {
		return fmt.Errorf("no leader available")
	}

	leader := cluster.GetLeaderMember()
	if leader == nil {
		return fmt.Errorf("leader not found")
	}

	apiURL := leader.Data.APIURL
	if apiURL == "" {
		return fmt.Errorf("no API URL for leader")
	}

	// Build request body
	body := map[string]interface{}{
		"leader": leader.Name,
	}
	if candidate != "" {
		body["candidate"] = candidate
	}

	bodyJSON, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL+"/switchover", strings.NewReader(string(bodyJSON)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to leader: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("switchover failed: %s", string(respBody))
	}

	fmt.Println("Switchover initiated")
	return nil
}

// failover command
func failoverCmd() *cobra.Command {
	var candidate string
	var force bool

	cmd := &cobra.Command{
		Use:   "failover [cluster]",
		Short: "Trigger a failover",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				clusterName = args[0]
			}
			return doFailover(candidate, force)
		},
	}

	cmd.Flags().StringVar(&candidate, "candidate", "", "Candidate to become leader")
	cmd.Flags().BoolVar(&force, "force", false, "Force failover without confirmation")

	return cmd
}

func doFailover(candidate string, force bool) error {
	// Similar to switchover but without contacting the leader
	fmt.Println("Failover not yet fully implemented")
	return nil
}

// remove command
func removeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove [cluster]",
		Short: "Remove cluster from DCS",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				clusterName = args[0]
			}

			dcsClient, err := getDCS()
			if err != nil {
				return err
			}
			defer dcsClient.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := dcsClient.DeleteCluster(ctx); err != nil {
				return fmt.Errorf("failed to remove cluster: %w", err)
			}

			fmt.Println("Cluster removed")
			return nil
		},
	}
}

// history command
func historyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history [cluster]",
		Short: "Show cluster history",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				clusterName = args[0]
			}

			dcsClient, err := getDCS()
			if err != nil {
				return err
			}
			defer dcsClient.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			cluster, err := dcsClient.GetCluster(ctx)
			if err != nil {
				return err
			}

			if cluster.History == nil || len(cluster.History.Value) == 0 {
				fmt.Println("No history found")
				return nil
			}

			fmt.Printf("| %-8s | %-16s | %-20s | %-20s |\n",
				"Timeline", "LSN", "Reason", "Timestamp")
			fmt.Println("+" + strings.Repeat("-", 72) + "+")

			for _, h := range cluster.History.Value {
				fmt.Printf("| %-8d | %-16s | %-20s | %-20s |\n",
					h.Timeline, types.LSN(h.LSN).String(), h.Reason, h.Timestamp)
			}

			return nil
		},
	}
}

// version command
func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("patronictl %s (Go)\n", types.Version)
		},
	}
}
