// Package exhibitor provides a ZooKeeper DCS implementation with Exhibitor discovery.
//
// Exhibitor is a supervisor system for ZooKeeper that provides a REST API
// to discover the ZooKeeper ensemble members dynamically.
package exhibitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/patroni/patroni-go/internal/dcs"
	"github.com/patroni/patroni-go/internal/dcs/zookeeper"
	"github.com/patroni/patroni-go/pkg/types"
)

const (
	// DefaultTimeout is the default HTTP timeout for Exhibitor requests.
	DefaultTimeout = 3100 * time.Millisecond
	// DefaultPollInterval is the default interval between Exhibitor polls.
	DefaultPollInterval = 300 * time.Second
	// DefaultURIPath is the default Exhibitor cluster list endpoint.
	DefaultURIPath = "/exhibitor/v1/cluster/list"
)

// ExhibitorResponse represents the JSON response from Exhibitor's cluster list API.
type ExhibitorResponse struct {
	Servers []string `json:"servers"`
	Port    int      `json:"port"`
}

// EnsembleProvider discovers ZooKeeper ensemble members via Exhibitor.
type EnsembleProvider struct {
	mu             sync.RWMutex
	exhibitorPort  int
	uriPath        string
	pollInterval   time.Duration
	exhibitors     []string
	bootExhibitors []string
	zookeeperHosts string
	nextPoll       time.Time
	httpClient     *http.Client
}

// NewEnsembleProvider creates a new Exhibitor ensemble provider.
func NewEnsembleProvider(hosts []string, port int, uriPath string, pollInterval time.Duration) (*EnsembleProvider, error) {
	if len(hosts) == 0 {
		return nil, fmt.Errorf("no exhibitor hosts provided")
	}

	if uriPath == "" {
		uriPath = DefaultURIPath
	}

	if pollInterval == 0 {
		pollInterval = DefaultPollInterval
	}

	p := &EnsembleProvider{
		exhibitorPort:  port,
		uriPath:        uriPath,
		pollInterval:   pollInterval,
		exhibitors:     hosts,
		bootExhibitors: hosts,
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
	}

	// Initial poll to get ZooKeeper hosts
	for !p.Poll() {
		log.Info().Msg("waiting on exhibitor")
		time.Sleep(5 * time.Second)
	}

	return p, nil
}

// Poll queries Exhibitor for the current ZooKeeper ensemble.
// Returns true if the ensemble has changed.
func (p *EnsembleProvider) Poll() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.nextPoll.IsZero() && time.Now().Before(p.nextPoll) {
		return false
	}

	// Try current exhibitors first, then fall back to boot exhibitors
	resp := p.queryExhibitors(p.exhibitors)
	if resp == nil {
		resp = p.queryExhibitors(p.bootExhibitors)
	}

	if resp == nil || len(resp.Servers) == 0 || resp.Port == 0 {
		return false
	}

	p.nextPoll = time.Now().Add(p.pollInterval)

	// Sort servers for consistent comparison
	sortedServers := make([]string, len(resp.Servers))
	copy(sortedServers, resp.Servers)
	sort.Strings(sortedServers)

	// Build ZooKeeper connection string
	var hosts []string
	for _, server := range sortedServers {
		hosts = append(hosts, fmt.Sprintf("%s:%d", server, resp.Port))
	}
	newHosts := strings.Join(hosts, ",")

	if p.zookeeperHosts != newHosts {
		log.Info().
			Str("old", p.zookeeperHosts).
			Str("new", newHosts).
			Msg("ZooKeeper connection string has changed")
		p.zookeeperHosts = newHosts
		p.exhibitors = resp.Servers
		return true
	}

	return false
}

// queryExhibitors queries a list of Exhibitor hosts for the cluster info.
func (p *EnsembleProvider) queryExhibitors(exhibitors []string) *ExhibitorResponse {
	// Shuffle for load balancing
	shuffled := make([]string, len(exhibitors))
	copy(shuffled, exhibitors)
	rand.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	for _, host := range shuffled {
		url := fmt.Sprintf("http://%s:%d%s", host, p.exhibitorPort, p.uriPath)
		resp, err := p.httpClient.Get(url)
		if err != nil {
			log.Debug().Err(err).Str("host", host).Msg("Request to exhibitor failed")
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Debug().Int("status", resp.StatusCode).Str("host", host).Msg("Exhibitor returned non-200 status")
			continue
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Debug().Err(err).Str("host", host).Msg("Failed to read exhibitor response")
			continue
		}

		var exhibitorResp ExhibitorResponse
		if err := json.Unmarshal(body, &exhibitorResp); err != nil {
			log.Debug().Err(err).Str("host", host).Msg("Failed to parse exhibitor response")
			continue
		}

		return &exhibitorResp
	}

	return nil
}

// ZookeeperHosts returns the current ZooKeeper connection string.
func (p *EnsembleProvider) ZookeeperHosts() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.zookeeperHosts
}

// Config holds Exhibitor DCS configuration.
type Config struct {
	Hosts        []string
	Port         int
	URIPath      string
	PollInterval time.Duration
	// ZooKeeper-specific config
	Namespace string
	Scope     string
	Name      string
	TTL       int
}

// Exhibitor implements DCS using ZooKeeper with Exhibitor discovery.
type Exhibitor struct {
	dcs.DCS
	provider *EnsembleProvider
}

// New creates a new Exhibitor DCS.
func New(cfg *dcs.Config) (*Exhibitor, error) {
	// Parse exhibitor-specific config
	exhibitorHosts := cfg.Hosts
	exhibitorPort := 8181 // default exhibitor port
	pollInterval := DefaultPollInterval

	// Create ensemble provider
	provider, err := NewEnsembleProvider(
		exhibitorHosts,
		exhibitorPort,
		DefaultURIPath,
		pollInterval,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create exhibitor provider: %w", err)
	}

	// Create ZooKeeper DCS with discovered hosts
	zkCfg := &dcs.Config{
		Hosts:     strings.Split(provider.ZookeeperHosts(), ","),
		Namespace: cfg.Namespace,
		Scope:     cfg.Scope,
		Name:      cfg.Name,
		TTL:       cfg.TTL,
	}

	zk, err := zookeeper.New(zkCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create zookeeper client: %w", err)
	}

	return &Exhibitor{
		DCS:      zk,
		provider: provider,
	}, nil
}

// Name returns the DCS type name.
func (e *Exhibitor) Name() string {
	return "exhibitor"
}

// GetCluster retrieves the cluster state, polling Exhibitor for ensemble changes.
func (e *Exhibitor) GetCluster(ctx context.Context) (*types.Cluster, error) {
	// Check for ensemble changes
	if e.provider.Poll() {
		newHosts := e.provider.ZookeeperHosts()
		log.Info().Str("hosts", newHosts).Msg("Updating ZooKeeper connection")
		// Note: In a full implementation, we'd reconnect to the new hosts
	}

	return e.DCS.GetCluster(ctx)
}

// Close closes the Exhibitor DCS connection.
func (e *Exhibitor) Close() error {
	return e.DCS.Close()
}
