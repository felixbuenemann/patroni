// Package etcd provides an etcd v2 API DCS implementation for Patroni.
//
// Note: etcd v2 API is deprecated. Consider using etcd v3 (internal/dcs/etcd3) instead.
package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/patroni/patroni-go/internal/dcs"
	"github.com/patroni/patroni-go/pkg/types"
)

// EtcdError represents an etcd-specific error.
type EtcdError struct {
	ErrorCode int    `json:"errorCode"`
	Message   string `json:"message"`
	Cause     string `json:"cause"`
	Index     uint64 `json:"index"`
}

func (e *EtcdError) Error() string {
	return fmt.Sprintf("etcd error %d: %s (cause: %s)", e.ErrorCode, e.Message, e.Cause)
}

// Error codes from etcd v2 API
const (
	ErrCodeKeyNotFound      = 100
	ErrCodeTestFailed       = 101
	ErrCodeNotFile          = 102
	ErrCodeNotDir           = 104
	ErrCodeNodeExist        = 105
	ErrCodeRootReadOnly     = 107
	ErrCodeDirNotEmpty      = 108
	ErrCodePrevValueReq     = 201
	ErrCodeTTLNaN           = 202
	ErrCodeIndexNaN         = 203
	ErrCodeInvalidField     = 209
	ErrCodeRaftInternal     = 300
	ErrCodeLeaderElect      = 301
	ErrCodeWatcherCleared   = 400
	ErrCodeEventIndexCleared = 401
)

// Node represents an etcd node in the response.
type Node struct {
	Key           string  `json:"key"`
	Value         string  `json:"value,omitempty"`
	Dir           bool    `json:"dir,omitempty"`
	Expiration    *string `json:"expiration,omitempty"`
	TTL           int64   `json:"ttl,omitempty"`
	ModifiedIndex uint64  `json:"modifiedIndex"`
	CreatedIndex  uint64  `json:"createdIndex"`
	Nodes         []Node  `json:"nodes,omitempty"`
}

// Response represents an etcd API response.
type Response struct {
	Action   string `json:"action"`
	Node     *Node  `json:"node,omitempty"`
	PrevNode *Node  `json:"prevNode,omitempty"`
}

// Config holds etcd client configuration.
type Config struct {
	Hosts        []string
	SRV          string
	SRVSuffix    string
	Protocol     string
	Username     string
	Password     string
	CACert       string
	Cert         string
	Key          string
	RetryTimeout time.Duration
	TTL          int
}

// Client is an etcd v2 API client.
type Client struct {
	mu            sync.RWMutex
	config        *Config
	httpClient    *http.Client
	machinesCache []string
	baseURI       string
	cacheUpdated  time.Time
	cacheTTL      time.Duration
}

// NewClient creates a new etcd v2 client.
func NewClient(cfg *Config) (*Client, error) {
	if len(cfg.Hosts) == 0 && cfg.SRV == "" {
		return nil, fmt.Errorf("no etcd hosts or SRV record configured")
	}

	if cfg.Protocol == "" {
		cfg.Protocol = "http"
	}

	if cfg.RetryTimeout == 0 {
		cfg.RetryTimeout = 10 * time.Second
	}

	if cfg.TTL == 0 {
		cfg.TTL = 30
	}

	client := &Client{
		config:   cfg,
		cacheTTL: time.Duration(cfg.TTL) * 10 * time.Second,
		httpClient: &http.Client{
			Timeout: cfg.RetryTimeout,
		},
	}

	// Initialize machines cache
	if err := client.loadMachinesCache(); err != nil {
		return nil, err
	}

	return client, nil
}

// loadMachinesCache loads the initial machines cache from config or SRV.
func (c *Client) loadMachinesCache() error {
	var machines []string

	if c.config.SRV != "" {
		machines = c.getMachinesFromSRV()
	}

	if len(machines) == 0 && len(c.config.Hosts) > 0 {
		for _, host := range c.config.Hosts {
			machines = append(machines, fmt.Sprintf("%s://%s", c.config.Protocol, host))
		}
	}

	if len(machines) == 0 {
		return fmt.Errorf("no etcd machines available")
	}

	// Shuffle for load balancing
	rand.Shuffle(len(machines), func(i, j int) {
		machines[i], machines[j] = machines[j], machines[i]
	})

	c.mu.Lock()
	c.machinesCache = machines
	c.baseURI = machines[0]
	c.cacheUpdated = time.Now()
	c.mu.Unlock()

	return nil
}

// getMachinesFromSRV resolves etcd machines from SRV records.
func (c *Client) getMachinesFromSRV() []string {
	var machines []string
	suffixes := []string{"-client-ssl", "-client", "-ssl", "", "-server-ssl", "-server"}

	for _, suffix := range suffixes {
		srv := fmt.Sprintf("_etcd%s._tcp.%s", suffix, c.config.SRV)
		if c.config.SRVSuffix != "" {
			srv = fmt.Sprintf("_etcd%s-%s._tcp.%s", suffix, c.config.SRVSuffix, c.config.SRV)
		}

		_, addrs, err := net.LookupSRV("", "", srv)
		if err != nil || len(addrs) == 0 {
			continue
		}

		protocol := "http"
		if strings.Contains(suffix, "ssl") {
			protocol = "https"
		}

		for _, addr := range addrs {
			host := strings.TrimSuffix(addr.Target, ".")
			machines = append(machines, fmt.Sprintf("%s://%s:%d", protocol, host, addr.Port))
		}

		if len(machines) > 0 {
			break
		}
	}

	return machines
}

// refreshMachinesCache updates the machines cache from the cluster.
func (c *Client) refreshMachinesCache() error {
	c.mu.RLock()
	machines := c.machinesCache
	c.mu.RUnlock()

	for _, baseURI := range machines {
		resp, err := c.httpClient.Get(baseURI + "/v2/members")
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			continue
		}

		var membersResp struct {
			Members []struct {
				ClientURLs []string `json:"clientURLs"`
			} `json:"members"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&membersResp); err != nil {
			continue
		}

		var newMachines []string
		for _, m := range membersResp.Members {
			newMachines = append(newMachines, m.ClientURLs...)
		}

		if len(newMachines) > 0 {
			rand.Shuffle(len(newMachines), func(i, j int) {
				newMachines[i], newMachines[j] = newMachines[j], newMachines[i]
			})

			c.mu.Lock()
			c.machinesCache = newMachines
			c.cacheUpdated = time.Now()
			c.mu.Unlock()

			return nil
		}
	}

	return fmt.Errorf("failed to refresh machines cache")
}

// do performs an HTTP request with retry logic.
func (c *Client) do(ctx context.Context, method, path string, params url.Values) (*Response, error) {
	c.mu.RLock()
	machines := c.machinesCache
	baseURI := c.baseURI
	cacheAge := time.Since(c.cacheUpdated)
	c.mu.RUnlock()

	// Refresh cache if needed
	if cacheAge > c.cacheTTL {
		c.refreshMachinesCache()
	}

	// Try each machine
	orderedMachines := append([]string{baseURI}, machines...)
	seen := make(map[string]bool)

	for _, machine := range orderedMachines {
		if seen[machine] {
			continue
		}
		seen[machine] = true

		resp, etcdErr, err := c.doRequest(ctx, method, machine+"/v2/keys"+path, params)
		if err != nil {
			log.Debug().Err(err).Str("machine", machine).Msg("request failed")
			continue
		}

		if etcdErr != nil {
			return nil, etcdErr
		}

		// Update base URI on success
		if machine != baseURI {
			c.mu.Lock()
			c.baseURI = machine
			c.mu.Unlock()
		}

		return resp, nil
	}

	return nil, fmt.Errorf("no etcd machines available")
}

// doRequest performs a single HTTP request.
func (c *Client) doRequest(ctx context.Context, method, urlStr string, params url.Values) (*Response, *EtcdError, error) {
	var body io.Reader
	if method == http.MethodPut || method == http.MethodPost {
		body = strings.NewReader(params.Encode())
	} else if len(params) > 0 {
		urlStr += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return nil, nil, err
	}

	if method == http.MethodPut || method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	if c.config.Username != "" && c.config.Password != "" {
		req.SetBasicAuth(c.config.Username, c.config.Password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	if resp.StatusCode >= 400 {
		var etcdErr EtcdError
		if err := json.Unmarshal(data, &etcdErr); err == nil {
			return nil, &etcdErr, nil
		}
		return nil, nil, fmt.Errorf("etcd returned status %d: %s", resp.StatusCode, string(data))
	}

	var etcdResp Response
	if err := json.Unmarshal(data, &etcdResp); err != nil {
		return nil, nil, err
	}

	return &etcdResp, nil, nil
}

// Get retrieves a key from etcd.
func (c *Client) Get(ctx context.Context, key string, recursive bool) (*Response, error) {
	params := url.Values{}
	if recursive {
		params.Set("recursive", "true")
	}
	return c.do(ctx, http.MethodGet, key, params)
}

// Set sets a key in etcd.
func (c *Client) Set(ctx context.Context, key, value string, ttl int) (*Response, error) {
	params := url.Values{}
	params.Set("value", value)
	if ttl > 0 {
		params.Set("ttl", fmt.Sprintf("%d", ttl))
	}
	return c.do(ctx, http.MethodPut, key, params)
}

// Create creates a new key if it doesn't exist.
func (c *Client) Create(ctx context.Context, key, value string, ttl int) (*Response, error) {
	params := url.Values{}
	params.Set("value", value)
	params.Set("prevExist", "false")
	if ttl > 0 {
		params.Set("ttl", fmt.Sprintf("%d", ttl))
	}
	return c.do(ctx, http.MethodPut, key, params)
}

// Update updates an existing key.
func (c *Client) Update(ctx context.Context, key, value string, ttl int, prevValue string, prevIndex uint64) (*Response, error) {
	params := url.Values{}
	params.Set("value", value)
	params.Set("prevExist", "true")
	if ttl > 0 {
		params.Set("ttl", fmt.Sprintf("%d", ttl))
	}
	if prevValue != "" {
		params.Set("prevValue", prevValue)
	}
	if prevIndex > 0 {
		params.Set("prevIndex", fmt.Sprintf("%d", prevIndex))
	}
	return c.do(ctx, http.MethodPut, key, params)
}

// Delete removes a key from etcd.
func (c *Client) Delete(ctx context.Context, key string, recursive bool, prevValue string, prevIndex uint64) (*Response, error) {
	params := url.Values{}
	if recursive {
		params.Set("recursive", "true")
	}
	if prevValue != "" {
		params.Set("prevValue", prevValue)
	}
	if prevIndex > 0 {
		params.Set("prevIndex", fmt.Sprintf("%d", prevIndex))
	}
	return c.do(ctx, http.MethodDelete, key, params)
}

// Watch watches a key for changes.
func (c *Client) Watch(ctx context.Context, key string, waitIndex uint64, recursive bool) (*Response, error) {
	params := url.Values{}
	params.Set("wait", "true")
	if waitIndex > 0 {
		params.Set("waitIndex", fmt.Sprintf("%d", waitIndex))
	}
	if recursive {
		params.Set("recursive", "true")
	}
	return c.do(ctx, http.MethodGet, key, params)
}

// Etcd implements the DCS interface using etcd v2 API.
type Etcd struct {
	client    *Client
	config    *dcs.Config
	namespace string
	scope     string
	name      string
	ttl       int
}

// New creates a new etcd v2 DCS.
func New(cfg *dcs.Config) (*Etcd, error) {
	etcdCfg := &Config{
		Hosts:        cfg.Hosts,
		RetryTimeout: time.Duration(cfg.RetryTimeout) * time.Second,
		TTL:          cfg.TTL,
	}

	client, err := NewClient(etcdCfg)
	if err != nil {
		return nil, err
	}

	return &Etcd{
		client:    client,
		config:    cfg,
		namespace: cfg.Namespace,
		scope:     cfg.Scope,
		name:      cfg.Name,
		ttl:       cfg.TTL,
	}, nil
}

// Name returns the DCS type name.
func (e *Etcd) Name() string {
	return "etcd"
}

// BasePath returns the base path for this cluster.
func (e *Etcd) BasePath() string {
	return fmt.Sprintf("%s/%s", e.namespace, e.scope)
}

// GetCluster retrieves the cluster state from etcd.
func (e *Etcd) GetCluster(ctx context.Context) (*types.Cluster, error) {
	resp, err := e.client.Get(ctx, e.BasePath(), true)
	if err != nil {
		if etcdErr, ok := err.(*EtcdError); ok && etcdErr.ErrorCode == ErrCodeKeyNotFound {
			return &types.Cluster{}, nil
		}
		return nil, err
	}

	return e.clusterFromNodes(resp.Node)
}

// clusterFromNodes builds a Cluster from etcd nodes.
func (e *Etcd) clusterFromNodes(root *Node) (*types.Cluster, error) {
	if root == nil || !root.Dir {
		return &types.Cluster{}, nil
	}

	nodes := make(map[string]*Node)
	for i := range root.Nodes {
		node := &root.Nodes[i]
		key := strings.TrimPrefix(node.Key, root.Key)
		key = strings.TrimPrefix(key, "/")
		nodes[key] = node
	}

	cluster := &types.Cluster{}

	// Parse initialize
	if node, ok := nodes["initialize"]; ok && node.Value != "" {
		cluster.InitializeVersion = int64(node.ModifiedIndex)
	}

	// Parse config
	if node, ok := nodes["config"]; ok {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(node.Value), &data); err == nil {
			cluster.Config = &types.ClusterConfig{
				Version: int64(node.ModifiedIndex),
				Data:    data,
			}
		}
	}

	// Parse history
	if node, ok := nodes["history"]; ok {
		var entries []types.TimelineEntry
		if err := json.Unmarshal([]byte(node.Value), &entries); err == nil {
			cluster.History = &types.TimelineHistory{
				Version: int64(node.ModifiedIndex),
				Value:   entries,
			}
		}
	}

	// Parse leader
	if node, ok := nodes["leader"]; ok {
		cluster.Leader = &types.Leader{
			Version:    int64(node.ModifiedIndex),
			MemberName: node.Value,
		}
	}

	// Parse members
	if membersNode, ok := nodes["members"]; ok && membersNode.Dir {
		for _, memberNode := range membersNode.Nodes {
			name := strings.TrimPrefix(memberNode.Key, membersNode.Key+"/")
			member, _ := types.ParseMemberFromJSON(
				int64(memberNode.ModifiedIndex),
				name,
				"", // session not available in v2 API
				memberNode.Value,
			)
			if member != nil {
				cluster.Members = append(cluster.Members, member)
			}
		}
	}

	// Parse sync state
	if node, ok := nodes["sync"]; ok {
		var syncState types.SyncState
		if err := json.Unmarshal([]byte(node.Value), &syncState); err == nil {
			syncState.Version = int64(node.ModifiedIndex)
			cluster.SyncState = &syncState
		}
	}

	// Parse failover
	if node, ok := nodes["failover"]; ok {
		var failover types.Failover
		if err := json.Unmarshal([]byte(node.Value), &failover); err == nil {
			failover.Version = int64(node.ModifiedIndex)
			cluster.Failover = &failover
		}
	}

	return cluster, nil
}

// TouchMember updates the member's TTL in etcd.
func (e *Etcd) TouchMember(ctx context.Context, data map[string]interface{}) error {
	value, err := json.Marshal(data)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("%s/members/%s", e.BasePath(), e.name)
	_, err = e.client.Set(ctx, path, string(value), e.ttl)
	return err
}

// AttemptToAcquireLeader attempts to acquire the leader lock.
func (e *Etcd) AttemptToAcquireLeader(ctx context.Context) (bool, error) {
	path := fmt.Sprintf("%s/leader", e.BasePath())
	_, err := e.client.Create(ctx, path, e.name, e.ttl)
	if err != nil {
		if etcdErr, ok := err.(*EtcdError); ok && etcdErr.ErrorCode == ErrCodeNodeExist {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// UpdateLeader updates the leader lock TTL.
func (e *Etcd) UpdateLeader(ctx context.Context, leader *types.Leader) (bool, error) {
	path := fmt.Sprintf("%s/leader", e.BasePath())
	_, err := e.client.Update(ctx, path, e.name, e.ttl, e.name, 0)
	if err != nil {
		if etcdErr, ok := err.(*EtcdError); ok && etcdErr.ErrorCode == ErrCodeKeyNotFound {
			// Key doesn't exist, try to create it
			return e.AttemptToAcquireLeader(ctx)
		}
		return false, err
	}
	return true, nil
}

// DeleteLeader releases the leader lock.
func (e *Etcd) DeleteLeader(ctx context.Context, leader *types.Leader) error {
	path := fmt.Sprintf("%s/leader", e.BasePath())
	_, err := e.client.Delete(ctx, path, false, e.name, 0)
	return err
}

// SetTTL sets the TTL for member operations.
func (e *Etcd) SetTTL(ttl int) {
	e.ttl = ttl
}

// Close closes the etcd client.
func (e *Etcd) Close() error {
	return nil
}
