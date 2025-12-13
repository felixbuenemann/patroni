// Package main implements the patroni_aws callback script for AWS EC2/EBS tagging.
//
// This script is used as a callback on role changes to tag EC2 instances and
// EBS volumes with the current cluster role (primary/replica).
//
// Usage:
//
//	patroni_aws <action> <role> <cluster_name>
//
// Where action is one of: on_start, on_stop, on_role_change
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	// IMDSTimeout is the timeout for IMDS requests
	IMDSTimeout = 2100 * time.Millisecond
	// RetryDeadline is the maximum time to retry AWS operations
	RetryDeadline = 300 * time.Second
	// MaxRetryDelay is the maximum delay between retries
	MaxRetryDelay = 30 * time.Second
)

// AWSConnection manages AWS EC2 operations for Patroni callbacks.
type AWSConnection struct {
	available   bool
	clusterName string
	instanceID  string
	region      string
}

// InstanceIdentityDocument represents the EC2 instance identity document.
type InstanceIdentityDocument struct {
	InstanceID string `json:"instanceId"`
	Region     string `json:"region"`
}

// NewAWSConnection creates a new AWS connection by fetching instance metadata.
func NewAWSConnection(clusterName string) *AWSConnection {
	conn := &AWSConnection{
		clusterName: clusterName,
		available:   false,
	}

	if clusterName == "" {
		conn.clusterName = "unknown"
	}

	ctx, cancel := context.WithTimeout(context.Background(), IMDSTimeout)
	defer cancel()

	// Try to get instance identity document from IMDS
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Error().Err(err).Msg("cannot load AWS config")
		return conn
	}

	imdsClient := imds.NewFromConfig(cfg)
	output, err := imdsClient.GetInstanceIdentityDocument(ctx, &imds.GetInstanceIdentityDocumentInput{})
	if err != nil {
		// Fallback to direct HTTP request to IMDS
		if !conn.fetchMetadataDirectly() {
			log.Error().Msg("cannot query AWS meta-data")
			return conn
		}
	} else {
		conn.instanceID = output.InstanceID
		conn.region = output.Region
	}

	if conn.instanceID != "" && conn.region != "" {
		conn.available = true
	}

	return conn
}

// fetchMetadataDirectly attempts to fetch instance metadata via direct HTTP.
func (c *AWSConnection) fetchMetadataDirectly() bool {
	client := &http.Client{Timeout: IMDSTimeout}

	// Get IMDSv2 token
	tokenReq, err := http.NewRequest(http.MethodPut, "http://169.254.169.254/latest/api/token", nil)
	if err != nil {
		return false
	}
	tokenReq.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "21600")

	tokenResp, err := client.Do(tokenReq)
	if err != nil {
		return false
	}
	defer tokenResp.Body.Close()

	token, err := io.ReadAll(tokenResp.Body)
	if err != nil {
		return false
	}

	// Get instance identity document
	docReq, err := http.NewRequest(http.MethodGet,
		"http://169.254.169.254/latest/dynamic/instance-identity/document", nil)
	if err != nil {
		return false
	}
	docReq.Header.Set("X-aws-ec2-metadata-token", string(token))

	docResp, err := client.Do(docReq)
	if err != nil {
		return false
	}
	defer docResp.Body.Close()

	if docResp.StatusCode >= 400 {
		return false
	}

	var doc InstanceIdentityDocument
	if err := json.NewDecoder(docResp.Body).Decode(&doc); err != nil {
		log.Error().Err(err).Msg("unable to fetch instance id and region from AWS meta-data")
		return false
	}

	c.instanceID = doc.InstanceID
	c.region = doc.Region
	return true
}

// Available returns whether AWS is available.
func (c *AWSConnection) Available() bool {
	return c.available
}

// OnRoleChange handles role change events by tagging EC2 and EBS resources.
func (c *AWSConnection) OnRoleChange(newRole string) bool {
	if !c.available {
		return false
	}

	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(c.region))
	if err != nil {
		log.Error().Err(err).Msg("failed to load AWS config")
		return false
	}

	client := ec2.NewFromConfig(cfg)

	// Tag EC2 instance
	if err := c.tagEC2WithRetry(ctx, client, newRole); err != nil {
		log.Warn().Err(err).
			Str("instance_id", c.instanceID).
			Msg("Unable to communicate to AWS when setting tags for the EC2 instance")
		return false
	}

	// Tag EBS volumes
	if err := c.tagEBSWithRetry(ctx, client, newRole); err != nil {
		log.Warn().Err(err).
			Str("instance_id", c.instanceID).
			Msg("Unable to communicate to AWS when setting tags for attached EBS volumes")
		return false
	}

	return true
}

// tagEC2WithRetry tags the EC2 instance with retry logic.
func (c *AWSConnection) tagEC2WithRetry(ctx context.Context, client *ec2.Client, role string) error {
	tags := []types.Tag{
		{Key: strPtr("Role"), Value: strPtr(role)},
	}

	return c.retryWithBackoff(ctx, func() error {
		_, err := client.CreateTags(ctx, &ec2.CreateTagsInput{
			Resources: []string{c.instanceID},
			Tags:      tags,
		})
		return err
	})
}

// tagEBSWithRetry tags attached EBS volumes with retry logic.
func (c *AWSConnection) tagEBSWithRetry(ctx context.Context, client *ec2.Client, role string) error {
	return c.retryWithBackoff(ctx, func() error {
		// Get attached volumes
		volumes, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
			Filters: []types.Filter{
				{
					Name:   strPtr("attachment.instance-id"),
					Values: []string{c.instanceID},
				},
			},
		})
		if err != nil {
			return err
		}

		if len(volumes.Volumes) == 0 {
			return nil
		}

		// Collect volume IDs
		volumeIDs := make([]string, 0, len(volumes.Volumes))
		for _, v := range volumes.Volumes {
			if v.VolumeId != nil {
				volumeIDs = append(volumeIDs, *v.VolumeId)
			}
		}

		// Tag volumes
		tags := []types.Tag{
			{Key: strPtr("Name"), Value: strPtr("spilo_" + c.clusterName)},
			{Key: strPtr("Role"), Value: strPtr(role)},
			{Key: strPtr("Instance"), Value: strPtr(c.instanceID)},
		}

		_, err = client.CreateTags(ctx, &ec2.CreateTagsInput{
			Resources: volumeIDs,
			Tags:      tags,
		})
		return err
	})
}

// retryWithBackoff retries an operation with exponential backoff.
func (c *AWSConnection) retryWithBackoff(ctx context.Context, op func() error) error {
	deadline := time.Now().Add(RetryDeadline)
	delay := time.Second

	for {
		err := op()
		if err == nil {
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("retry deadline exceeded: %w", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		// Exponential backoff with max delay
		delay *= 2
		if delay > MaxRetryDelay {
			delay = MaxRetryDelay
		}
	}
}

func strPtr(s string) *string {
	return &s
}

func main() {
	// Setup logging
	log.Logger = zerolog.New(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: "2006-01-02 15:04:05",
	}).With().Timestamp().Logger()

	if len(os.Args) != 4 {
		fmt.Fprintf(os.Stderr, "Usage: %s action role name\n", os.Args[0])
		os.Exit(1)
	}

	action := os.Args[1]
	role := os.Args[2]
	clusterName := os.Args[3]

	// Validate action
	switch action {
	case "on_start", "on_stop", "on_role_change":
		// Valid actions
	default:
		fmt.Fprintf(os.Stderr, "Usage: %s action role name\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "action must be one of: on_start, on_stop, on_role_change\n")
		os.Exit(1)
	}

	conn := NewAWSConnection(clusterName)
	if !conn.OnRoleChange(role) {
		os.Exit(1)
	}
}
