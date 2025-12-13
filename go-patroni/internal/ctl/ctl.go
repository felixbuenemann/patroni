// Package ctl provides the patronictl library for cluster management.
package ctl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/patroni/patroni-go/internal/dcs"
	"github.com/patroni/patroni-go/pkg/types"
)

// OutputFormat represents the output format for commands.
type OutputFormat string

const (
	FormatTable OutputFormat = "table"
	FormatJSON  OutputFormat = "json"
	FormatYAML  OutputFormat = "yaml"
	FormatTSV   OutputFormat = "tsv"
)

// MemberState represents a cluster member's state.
type MemberState string

const (
	StateRunning  MemberState = "running"
	StateStarting MemberState = "starting"
	StateStopped  MemberState = "stopped"
	StateUnknown  MemberState = "unknown"
)

// Client provides methods for interacting with a Patroni cluster.
type Client struct {
	dcs        dcs.DCS
	scope      string
	httpClient *http.Client
	timeout    time.Duration
}

// Config holds client configuration.
type Config struct {
	DCS       dcs.DCS
	Scope     string
	Timeout   time.Duration
}

// NewClient creates a new CTL client.
func NewClient(cfg *Config) *Client {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	return &Client{
		dcs:     cfg.DCS,
		scope:   cfg.Scope,
		timeout: timeout,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// ClusterMember represents a cluster member with status information.
type ClusterMember struct {
	Name     string      `json:"name"`
	Host     string      `json:"host"`
	Port     int         `json:"port"`
	Role     string      `json:"role"`
	State    MemberState `json:"state"`
	Timeline int         `json:"timeline"`
	Lag      int64       `json:"lag"`
	APIURL   string      `json:"api_url"`
	Tags     types.Tags  `json:"tags,omitempty"`
}

// ClusterInfo represents cluster information.
type ClusterInfo struct {
	Name       string           `json:"cluster"`
	Members    []*ClusterMember `json:"members"`
	Leader     string           `json:"leader"`
	Scope      string           `json:"scope"`
	Paused     bool             `json:"paused"`
}

// List returns the list of cluster members.
func (c *Client) List(ctx context.Context) (*ClusterInfo, error) {
	cluster, err := c.dcs.GetCluster(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	info := &ClusterInfo{
		Scope:   c.scope,
		Members: make([]*ClusterMember, 0, len(cluster.Members)),
	}

	if cluster.Leader != nil {
		info.Leader = cluster.Leader.MemberName
	}

	for _, m := range cluster.Members {
		member := &ClusterMember{
			Name:     m.Name,
			Role:     m.Data.Role,
			State:    MemberState(m.Data.State),
			Timeline: m.Data.Timeline,
			Tags:     m.Data.Tags,
		}

		// Parse host/port from connection URL
		if m.Data.ConnURL != "" {
			if u, err := url.Parse(m.Data.ConnURL); err == nil {
				member.Host = u.Hostname()
				if port := u.Port(); port != "" {
					fmt.Sscanf(port, "%d", &member.Port)
				}
			}
		}

		member.APIURL = m.Data.APIURL

		// Determine role
		if cluster.Leader != nil && cluster.Leader.MemberName == m.Name {
			member.Role = "Leader"
		} else if m.Data.Role == "replica" || m.Data.Role == "standby_leader" {
			member.Role = "Replica"
		}

		info.Members = append(info.Members, member)
	}

	// Sort members: leader first, then by name
	sort.Slice(info.Members, func(i, j int) bool {
		if info.Members[i].Name == info.Leader {
			return true
		}
		if info.Members[j].Name == info.Leader {
			return false
		}
		return info.Members[i].Name < info.Members[j].Name
	})

	return info, nil
}

// SwitchoverRequest represents a switchover request.
type SwitchoverRequest struct {
	Leader      string     `json:"leader,omitempty"`
	Candidate   string     `json:"candidate,omitempty"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
}

// Switchover performs a switchover operation.
func (c *Client) Switchover(ctx context.Context, req *SwitchoverRequest) error {
	if req.Leader == "" {
		return fmt.Errorf("leader is required for switchover")
	}

	// Find the leader's API URL
	cluster, err := c.dcs.GetCluster(ctx)
	if err != nil {
		return err
	}

	var apiURL string
	for _, m := range cluster.Members {
		if m.Name == req.Leader {
			apiURL = m.Data.APIURL
			break
		}
	}

	if apiURL == "" {
		return fmt.Errorf("could not find leader %s", req.Leader)
	}

	// Make the switchover request
	data, _ := json.Marshal(req)
	resp, err := c.postRequest(ctx, apiURL+"/switchover", data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("switchover failed: %s", string(body))
	}

	return nil
}

// FailoverRequest represents a failover request.
type FailoverRequest struct {
	Candidate string `json:"candidate,omitempty"`
}

// Failover performs a failover operation.
func (c *Client) Failover(ctx context.Context, req *FailoverRequest) error {
	if req.Candidate == "" {
		return fmt.Errorf("candidate is required for failover")
	}

	cluster, err := c.dcs.GetCluster(ctx)
	if err != nil {
		return err
	}

	// Find any member's API URL
	var apiURL string
	for _, m := range cluster.Members {
		if m.Data.APIURL != "" {
			apiURL = m.Data.APIURL
			break
		}
	}

	if apiURL == "" {
		return fmt.Errorf("could not find any cluster members")
	}

	data, _ := json.Marshal(req)
	resp, err := c.postRequest(ctx, apiURL+"/failover", data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failover failed: %s", string(body))
	}

	return nil
}

// ReinitRequest represents a reinitialize request.
type ReinitRequest struct {
	Force bool `json:"force,omitempty"`
}

// Reinit reinitializes a cluster member.
func (c *Client) Reinit(ctx context.Context, member string, req *ReinitRequest) error {
	cluster, err := c.dcs.GetCluster(ctx)
	if err != nil {
		return err
	}

	var apiURL string
	for _, m := range cluster.Members {
		if m.Name == member {
			apiURL = m.Data.APIURL
			break
		}
	}

	if apiURL == "" {
		return fmt.Errorf("could not find member %s", member)
	}

	data, _ := json.Marshal(req)
	resp, err := c.postRequest(ctx, apiURL+"/reinitialize", data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("reinitialize failed: %s", string(body))
	}

	return nil
}

// Restart restarts a cluster member.
func (c *Client) Restart(ctx context.Context, member string, scheduled *time.Time) error {
	cluster, err := c.dcs.GetCluster(ctx)
	if err != nil {
		return err
	}

	var apiURL string
	for _, m := range cluster.Members {
		if m.Name == member {
			apiURL = m.Data.APIURL
			break
		}
	}

	if apiURL == "" {
		return fmt.Errorf("could not find member %s", member)
	}

	data := map[string]interface{}{}
	if scheduled != nil {
		data["schedule"] = scheduled.Format(time.RFC3339)
	}

	body, _ := json.Marshal(data)
	resp, err := c.postRequest(ctx, apiURL+"/restart", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("restart failed: %s", string(respBody))
	}

	return nil
}

// Reload reloads a cluster member's configuration.
func (c *Client) Reload(ctx context.Context, member string) error {
	cluster, err := c.dcs.GetCluster(ctx)
	if err != nil {
		return err
	}

	var apiURL string
	for _, m := range cluster.Members {
		if m.Name == member {
			apiURL = m.Data.APIURL
			break
		}
	}

	if apiURL == "" {
		return fmt.Errorf("could not find member %s", member)
	}

	resp, err := c.postRequest(ctx, apiURL+"/reload", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("reload failed: %s", string(body))
	}

	return nil
}

// Pause pauses automatic failover.
func (c *Client) Pause(ctx context.Context, wait bool) error {
	return c.setPaused(ctx, true)
}

// Resume resumes automatic failover.
func (c *Client) Resume(ctx context.Context, wait bool) error {
	return c.setPaused(ctx, false)
}

func (c *Client) setPaused(ctx context.Context, paused bool) error {
	cluster, err := c.dcs.GetCluster(ctx)
	if err != nil {
		return err
	}

	var apiURL string
	if cluster.Leader != nil {
		for _, m := range cluster.Members {
			if m.Name == cluster.Leader.MemberName {
				apiURL = m.Data.APIURL
				break
			}
		}
	}

	if apiURL == "" && len(cluster.Members) > 0 {
		apiURL = cluster.Members[0].Data.APIURL
	}

	if apiURL == "" {
		return fmt.Errorf("could not find any cluster members")
	}

	data, _ := json.Marshal(map[string]interface{}{"paused": paused})
	resp, err := c.patchRequest(ctx, apiURL+"/config", data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to set paused state: %s", string(body))
	}

	return nil
}

// GetConfig returns the cluster configuration.
func (c *Client) GetConfig(ctx context.Context) (map[string]interface{}, error) {
	cluster, err := c.dcs.GetCluster(ctx)
	if err != nil {
		return nil, err
	}

	if cluster.Config == nil || cluster.Config.Data == nil {
		return make(map[string]interface{}), nil
	}

	return cluster.Config.Data, nil
}

// SetConfig sets the cluster configuration.
func (c *Client) SetConfig(ctx context.Context, config map[string]interface{}) error {
	cluster, err := c.dcs.GetCluster(ctx)
	if err != nil {
		return err
	}

	var apiURL string
	if cluster.Leader != nil {
		for _, m := range cluster.Members {
			if m.Name == cluster.Leader.MemberName {
				apiURL = m.Data.APIURL
				break
			}
		}
	}

	if apiURL == "" {
		return fmt.Errorf("could not find leader")
	}

	data, _ := json.Marshal(config)
	resp, err := c.putRequest(ctx, apiURL+"/config", data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to set config: %s", string(body))
	}

	return nil
}

// PatchConfig patches the cluster configuration.
func (c *Client) PatchConfig(ctx context.Context, patch map[string]interface{}) error {
	cluster, err := c.dcs.GetCluster(ctx)
	if err != nil {
		return err
	}

	var apiURL string
	if cluster.Leader != nil {
		for _, m := range cluster.Members {
			if m.Name == cluster.Leader.MemberName {
				apiURL = m.Data.APIURL
				break
			}
		}
	}

	if apiURL == "" {
		return fmt.Errorf("could not find leader")
	}

	data, _ := json.Marshal(patch)
	resp, err := c.patchRequest(ctx, apiURL+"/config", data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to patch config: %s", string(body))
	}

	return nil
}

// Remove removes a member from the cluster.
func (c *Client) Remove(ctx context.Context, member string) error {
	cluster, err := c.dcs.GetCluster(ctx)
	if err != nil {
		return err
	}

	var apiURL string
	for _, m := range cluster.Members {
		if m.Name == member {
			apiURL = m.Data.APIURL
			break
		}
	}

	if apiURL == "" {
		return fmt.Errorf("could not find member %s", member)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL+"/patroni", nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remove failed: %s", string(body))
	}

	return nil
}

// History returns the failover history.
func (c *Client) History(ctx context.Context) ([]types.TimelineEntry, error) {
	cluster, err := c.dcs.GetCluster(ctx)
	if err != nil {
		return nil, err
	}

	if cluster.History == nil {
		return []types.TimelineEntry{}, nil
	}

	return cluster.History.Value, nil
}

// FlushTarget represents what to flush.
type FlushTarget string

const (
	FlushRestart    FlushTarget = "restart"
	FlushSwitchover FlushTarget = "switchover"
)

// Flush flushes scheduled actions.
func (c *Client) Flush(ctx context.Context, member string, target FlushTarget) error {
	cluster, err := c.dcs.GetCluster(ctx)
	if err != nil {
		return err
	}

	var apiURL string
	for _, m := range cluster.Members {
		if m.Name == member {
			apiURL = m.Data.APIURL
			break
		}
	}

	if apiURL == "" {
		return fmt.Errorf("could not find member %s", member)
	}

	var endpoint string
	switch target {
	case FlushRestart:
		endpoint = "/restart"
	case FlushSwitchover:
		endpoint = "/switchover"
	default:
		return fmt.Errorf("unknown flush target: %s", target)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL+endpoint, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("flush failed: %s", string(body))
	}

	return nil
}

// Helper methods for HTTP requests

func (c *Client) postRequest(ctx context.Context, url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.httpClient.Do(req)
}

func (c *Client) putRequest(ctx context.Context, url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.httpClient.Do(req)
}

func (c *Client) patchRequest(ctx context.Context, url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.httpClient.Do(req)
}

// ParseDCSURL parses a DCS URL and returns the type and hosts.
func ParseDCSURL(dcsURL string) (string, []string, error) {
	u, err := url.Parse(dcsURL)
	if err != nil {
		return "", nil, err
	}

	dcsType := u.Scheme
	hosts := []string{u.Host}

	return dcsType, hosts, nil
}

// ValidateConfig validates a configuration key-value pair.
func ValidateConfig(key string, value interface{}) error {
	switch key {
	case "ttl":
		if v, ok := value.(int); ok && v < 10 {
			return fmt.Errorf("ttl must be at least 10")
		}
	case "loop_wait":
		if v, ok := value.(int); ok && v < 1 {
			return fmt.Errorf("loop_wait must be at least 1")
		}
	case "retry_timeout":
		if v, ok := value.(int); ok && v < 1 {
			return fmt.Errorf("retry_timeout must be at least 1")
		}
	}
	return nil
}
