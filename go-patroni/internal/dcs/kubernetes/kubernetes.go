// Package kubernetes provides a Kubernetes implementation of the DCS interface.
package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/patroni/patroni-go/internal/dcs"
	"github.com/patroni/patroni-go/pkg/types"
)

func init() {
	dcs.Register("kubernetes", New)
}

const (
	// Annotation keys
	annotationLeader    = "patroni.kubernetes.io/leader"
	annotationConfig    = "patroni.kubernetes.io/config"
	annotationSync      = "patroni.kubernetes.io/sync"
	annotationFailover  = "patroni.kubernetes.io/failover"
	annotationHistory   = "patroni.kubernetes.io/history"
	annotationStatus    = "patroni.kubernetes.io/status"
	annotationInitialize = "patroni.kubernetes.io/initialize"

	// Label keys
	labelCluster = "patroni.kubernetes.io/cluster"
	labelMember  = "patroni.kubernetes.io/member"
)

// Kubernetes implements the DCS interface using Kubernetes API.
type Kubernetes struct {
	*dcs.BaseDCS
	client     *kubernetes.Clientset
	namespace  string
	podName    string
	mu         sync.RWMutex
	leaderLease string
}

// New creates a new Kubernetes DCS instance.
func New(config *dcs.Config) (dcs.DCS, error) {
	// Build kubernetes config
	var k8sConfig *rest.Config
	var err error

	// Try in-cluster config first
	k8sConfig, err = rest.InClusterConfig()
	if err != nil {
		// Fall back to kubeconfig
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = os.Getenv("HOME") + "/.kube/config"
		}
		k8sConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to build kubernetes config: %w", err)
		}
	}

	client, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Get namespace
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = os.Getenv("PATRONI_KUBERNETES_NAMESPACE")
	}
	if namespace == "" {
		namespace = "default"
	}

	// Get pod name
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		podName = os.Getenv("HOSTNAME")
	}
	if podName == "" {
		podName = config.Name
	}

	k := &Kubernetes{
		BaseDCS:   dcs.NewBaseDCS(config),
		client:    client,
		namespace: namespace,
		podName:   podName,
	}

	k.SetSession(podName)

	return k, nil
}

// Name returns the DCS implementation name.
func (k *Kubernetes) Name() string {
	return "kubernetes"
}

// Close closes the Kubernetes client.
func (k *Kubernetes) Close() error {
	return nil
}

// configMapName returns the ConfigMap name for this cluster.
func (k *Kubernetes) configMapName() string {
	return "patroni-" + k.Scope
}

// endpointsName returns the Endpoints name for this cluster.
func (k *Kubernetes) endpointsName() string {
	return "patroni-" + k.Scope
}

// getOrCreateConfigMap gets or creates the cluster ConfigMap.
func (k *Kubernetes) getOrCreateConfigMap(ctx context.Context) (*corev1.ConfigMap, error) {
	cm, err := k.client.CoreV1().ConfigMaps(k.namespace).Get(ctx, k.configMapName(), metav1.GetOptions{})
	if err == nil {
		return cm, nil
	}

	// Create if not exists
	cm = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.configMapName(),
			Namespace: k.namespace,
			Labels: map[string]string{
				labelCluster: k.Scope,
			},
			Annotations: make(map[string]string),
		},
		Data: make(map[string]string),
	}

	return k.client.CoreV1().ConfigMaps(k.namespace).Create(ctx, cm, metav1.CreateOptions{})
}

// GetCluster retrieves the current cluster state from Kubernetes.
func (k *Kubernetes) GetCluster(ctx context.Context) (*types.Cluster, error) {
	cluster := &types.Cluster{}

	// Get ConfigMap for cluster data
	cm, err := k.getOrCreateConfigMap(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get configmap: %w", err)
	}

	// Parse annotations
	annotations := cm.Annotations
	if annotations == nil {
		annotations = make(map[string]string)
	}

	// Initialize
	if init, ok := cm.Data["initialize"]; ok {
		cluster.InitializeVersion = int64(cm.ResourceVersion[0]) // Use resource version as version
		_ = init
	}

	// Config
	if configData, ok := annotations[annotationConfig]; ok {
		config := &types.ClusterConfig{}
		if err := json.Unmarshal([]byte(configData), &config.Data); err == nil {
			cluster.Config = config
		}
	}

	// Leader
	if leaderData, ok := annotations[annotationLeader]; ok {
		leader := &types.Leader{}
		if err := json.Unmarshal([]byte(leaderData), leader); err == nil {
			cluster.Leader = leader
		} else {
			// Simple string format
			cluster.Leader = &types.Leader{MemberName: leaderData}
		}
	}

	// Sync state
	if syncData, ok := annotations[annotationSync]; ok {
		sync := &types.SyncState{}
		if err := json.Unmarshal([]byte(syncData), sync); err == nil {
			cluster.SyncState = sync
		}
	}

	// Failover
	if failoverData, ok := annotations[annotationFailover]; ok {
		failover := &types.Failover{}
		if err := json.Unmarshal([]byte(failoverData), failover); err == nil {
			cluster.Failover = failover
		}
	}

	// History
	if historyData, ok := annotations[annotationHistory]; ok {
		history := &types.TimelineHistory{}
		if err := json.Unmarshal([]byte(historyData), &history.Value); err == nil {
			cluster.History = history
		}
	}

	// Status
	if statusData, ok := annotations[annotationStatus]; ok {
		status := &types.Status{}
		if err := json.Unmarshal([]byte(statusData), status); err == nil {
			cluster.Status = status
		}
	}

	// Get members from pods
	pods, err := k.client.CoreV1().Pods(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", labelCluster, k.Scope),
	})
	if err != nil {
		log.Warn().Err(err).Msg("Failed to list pods")
	} else {
		for _, pod := range pods.Items {
			memberName := pod.Labels[labelMember]
			if memberName == "" {
				memberName = pod.Name
			}

			memberData := types.MemberData{}
			if data, ok := pod.Annotations["patroni.kubernetes.io/data"]; ok {
				json.Unmarshal([]byte(data), &memberData)
			}

			// Set connection info from pod
			if len(pod.Status.PodIP) > 0 {
				memberData.ConnURL = fmt.Sprintf("postgres://%s:5432/postgres", pod.Status.PodIP)
				memberData.APIURL = fmt.Sprintf("http://%s:8008", pod.Status.PodIP)
			}

			member := &types.Member{
				Version: int64(pod.Generation),
				Name:    memberName,
				Session: string(pod.UID),
				Data:    memberData,
			}

			cluster.Members = append(cluster.Members, member)
		}
	}

	return cluster, nil
}

// TouchMember updates this member's entry in Kubernetes.
func (k *Kubernetes) TouchMember(ctx context.Context, data *types.MemberData) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal member data: %w", err)
	}

	// Update pod annotations
	pod, err := k.client.CoreV1().Pods(k.namespace).Get(ctx, k.podName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get pod: %w", err)
	}

	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations["patroni.kubernetes.io/data"] = string(jsonData)

	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}
	pod.Labels[labelCluster] = k.Scope
	pod.Labels[labelMember] = k.Config.Name

	_, err = k.client.CoreV1().Pods(k.namespace).Update(ctx, pod, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update pod: %w", err)
	}

	return nil
}

// TakeLock attempts to acquire the leader lock.
func (k *Kubernetes) TakeLock(ctx context.Context) (bool, error) {
	cm, err := k.getOrCreateConfigMap(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get configmap: %w", err)
	}

	if cm.Annotations == nil {
		cm.Annotations = make(map[string]string)
	}

	// Check if there's already a leader
	if currentLeader, ok := cm.Annotations[annotationLeader]; ok && currentLeader != "" {
		var leader types.Leader
		if err := json.Unmarshal([]byte(currentLeader), &leader); err == nil {
			if leader.MemberName != k.Config.Name {
				return false, nil
			}
		}
	}

	// Set ourselves as leader
	leaderData, _ := json.Marshal(types.Leader{
		MemberName: k.Config.Name,
		Session:    k.podName,
	})
	cm.Annotations[annotationLeader] = string(leaderData)

	_, err = k.client.CoreV1().ConfigMaps(k.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to update leader: %w", err)
	}

	log.Info().Msg("Successfully acquired leader lock")
	return true, nil
}

// UpdateLeader updates the leader key.
func (k *Kubernetes) UpdateLeader(ctx context.Context, leaderInfo *types.MemberData) error {
	cm, err := k.getOrCreateConfigMap(ctx)
	if err != nil {
		return fmt.Errorf("failed to get configmap: %w", err)
	}

	if cm.Annotations == nil {
		cm.Annotations = make(map[string]string)
	}

	// Update leader annotation with timestamp
	leaderData, _ := json.Marshal(types.Leader{
		MemberName: k.Config.Name,
		Session:    k.podName,
	})
	cm.Annotations[annotationLeader] = string(leaderData)

	// Update status with optime
	if leaderInfo != nil && leaderInfo.XlogLocation > 0 {
		statusData, _ := json.Marshal(map[string]int64{"optime": leaderInfo.XlogLocation})
		cm.Annotations[annotationStatus] = string(statusData)
	}

	_, err = k.client.CoreV1().ConfigMaps(k.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

// DeleteLeader releases the leader lock.
func (k *Kubernetes) DeleteLeader(ctx context.Context) error {
	cm, err := k.getOrCreateConfigMap(ctx)
	if err != nil {
		return fmt.Errorf("failed to get configmap: %w", err)
	}

	if cm.Annotations == nil {
		return nil
	}

	// Check if we're the leader
	if currentLeader, ok := cm.Annotations[annotationLeader]; ok {
		var leader types.Leader
		if err := json.Unmarshal([]byte(currentLeader), &leader); err == nil {
			if leader.MemberName != k.Config.Name {
				return nil // Not our lock
			}
		}
	}

	delete(cm.Annotations, annotationLeader)

	_, err = k.client.CoreV1().ConfigMaps(k.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete leader: %w", err)
	}

	log.Info().Msg("Released leader lock")
	return nil
}

// AttemptToAcquireOrRenewLock tries to acquire or renew the leader lock.
func (k *Kubernetes) AttemptToAcquireOrRenewLock(ctx context.Context) (bool, error) {
	cm, err := k.getOrCreateConfigMap(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get configmap: %w", err)
	}

	if cm.Annotations == nil {
		cm.Annotations = make(map[string]string)
	}

	// Check if we're already the leader
	if currentLeader, ok := cm.Annotations[annotationLeader]; ok {
		var leader types.Leader
		if err := json.Unmarshal([]byte(currentLeader), &leader); err == nil {
			if leader.MemberName == k.Config.Name {
				// Renew
				return true, k.UpdateLeader(ctx, nil)
			}
		}
	}

	// Try to acquire
	return k.TakeLock(ctx)
}

// SetFailoverValue sets/updates the failover key.
func (k *Kubernetes) SetFailoverValue(ctx context.Context, failover *types.Failover) error {
	data, err := json.Marshal(failover)
	if err != nil {
		return fmt.Errorf("failed to marshal failover: %w", err)
	}

	return k.setAnnotation(ctx, annotationFailover, string(data))
}

// DeleteFailover deletes the failover key.
func (k *Kubernetes) DeleteFailover(ctx context.Context) error {
	return k.deleteAnnotation(ctx, annotationFailover)
}

// SetConfigValue sets/updates the cluster configuration.
func (k *Kubernetes) SetConfigValue(ctx context.Context, config map[string]interface{}) error {
	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return k.setAnnotation(ctx, annotationConfig, string(data))
}

// SetSyncState sets the synchronous replication state.
func (k *Kubernetes) SetSyncState(ctx context.Context, state *types.SyncState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal sync state: %w", err)
	}

	return k.setAnnotation(ctx, annotationSync, string(data))
}

// DeleteSyncState deletes the sync state.
func (k *Kubernetes) DeleteSyncState(ctx context.Context) error {
	return k.deleteAnnotation(ctx, annotationSync)
}

// Initialize initializes the cluster in DCS.
func (k *Kubernetes) Initialize(ctx context.Context, sysID string) (bool, error) {
	cm, err := k.getOrCreateConfigMap(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get configmap: %w", err)
	}

	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}

	// Check if already initialized
	if _, ok := cm.Data["initialize"]; ok {
		return false, nil
	}

	cm.Data["initialize"] = sysID

	_, err = k.client.CoreV1().ConfigMaps(k.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to initialize: %w", err)
	}

	return true, nil
}

// SetHistoryValue sets the timeline history.
func (k *Kubernetes) SetHistoryValue(ctx context.Context, history []types.TimelineEntry) error {
	data, err := json.Marshal(history)
	if err != nil {
		return fmt.Errorf("failed to marshal history: %w", err)
	}

	return k.setAnnotation(ctx, annotationHistory, string(data))
}

// DeleteCluster removes all cluster data from DCS.
func (k *Kubernetes) DeleteCluster(ctx context.Context) error {
	err := k.client.CoreV1().ConfigMaps(k.namespace).Delete(ctx, k.configMapName(), metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete configmap: %w", err)
	}
	return nil
}

// Watch returns a channel that receives cluster updates.
func (k *Kubernetes) Watch(ctx context.Context, timeout time.Duration) (<-chan *types.Cluster, error) {
	ch := make(chan *types.Cluster, 1)

	go func() {
		defer close(ch)

		// Send initial cluster state
		cluster, err := k.GetCluster(ctx)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get initial cluster state")
			return
		}
		select {
		case ch <- cluster:
		case <-ctx.Done():
			return
		}

		// Watch ConfigMap
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			watcher, err := k.client.CoreV1().ConfigMaps(k.namespace).Watch(ctx, metav1.ListOptions{
				FieldSelector: fmt.Sprintf("metadata.name=%s", k.configMapName()),
			})
			if err != nil {
				log.Error().Err(err).Msg("Failed to create watch")
				time.Sleep(time.Second)
				continue
			}

			for event := range watcher.ResultChan() {
				if event.Type == watch.Error {
					log.Error().Msg("Watch error")
					continue
				}

				cluster, err := k.GetCluster(ctx)
				if err != nil {
					log.Error().Err(err).Msg("Failed to get cluster after watch")
					continue
				}

				select {
				case ch <- cluster:
				case <-ctx.Done():
					watcher.Stop()
					return
				default:
				}
			}
		}
	}()

	return ch, nil
}

// setAnnotation sets an annotation on the ConfigMap.
func (k *Kubernetes) setAnnotation(ctx context.Context, key, value string) error {
	cm, err := k.getOrCreateConfigMap(ctx)
	if err != nil {
		return err
	}

	if cm.Annotations == nil {
		cm.Annotations = make(map[string]string)
	}
	cm.Annotations[key] = value

	_, err = k.client.CoreV1().ConfigMaps(k.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

// deleteAnnotation deletes an annotation from the ConfigMap.
func (k *Kubernetes) deleteAnnotation(ctx context.Context, key string) error {
	cm, err := k.getOrCreateConfigMap(ctx)
	if err != nil {
		return err
	}

	if cm.Annotations == nil {
		return nil
	}
	delete(cm.Annotations, key)

	_, err = k.client.CoreV1().ConfigMaps(k.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	return err
}
