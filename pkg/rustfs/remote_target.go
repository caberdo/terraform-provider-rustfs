// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strings"
)

// RemoteTargetCredentials holds the access credentials for a remote target.
type RemoteTargetCredentials struct {
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}

// RemoteTarget describes a remote replication target registered on a source
// bucket. The ARN is assigned by the server and never read back from list.
type RemoteTarget struct {
	Arn          string                   `json:"arn,omitempty"`
	Type         string                   `json:"type"`
	Endpoint     string                   `json:"endpoint"`
	Secure       bool                     `json:"secure"`
	Region       string                   `json:"region,omitempty"`
	Path         string                   `json:"path,omitempty"`
	SourceBucket string                   `json:"sourcebucket,omitempty"`
	TargetBucket string                   `json:"targetbucket"`
	Credentials  *RemoteTargetCredentials `json:"credentials,omitempty"`
	// DeploymentID is the site-replication peer deployment id the server
	// records on a target it derives from the site topology. It is empty for
	// an ordinary target, and RustFS 1.0.5 refuses connection-settings changes
	// on a target that carries one.
	DeploymentID string `json:"deployment_id,omitempty"`
}

// UnmarshalJSON decodes a listed target. RustFS reports the site-replication
// peer deployment id as `deployment_id` and accepts `deploymentID` as an alias
// for the same field (MinIO-written metadata uses the madmin spelling), so both
// are folded into DeploymentID and a caller never has to know which spelling
// the server used.
func (t *RemoteTarget) UnmarshalJSON(data []byte) error {
	type plainTarget RemoteTarget
	var decoded struct {
		plainTarget
		DeploymentIDAlias string `json:"deploymentID"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*t = RemoteTarget(decoded.plainTarget)
	if t.DeploymentID == "" {
		t.DeploymentID = decoded.DeploymentIDAlias
	}
	return nil
}

// PeerDeploymentID returns the site-replication peer deployment id recorded on
// the target: the id the server stores, or the peer segment of a target ARN
// RustFS derived from the site topology when no id was recorded. An empty value
// means the target is not known to be owned by a peer.
func (t RemoteTarget) PeerDeploymentID() string {
	if t.DeploymentID != "" {
		return t.DeploymentID
	}
	return replicationARNPeer(t.Arn)
}

// PeerOwned reports whether the target belongs to a site-replication peer. Such
// a target follows the site topology: RustFS 1.0.5 refuses to change its
// endpoint, credentials or TLS settings through the remote-target route and
// points at a site-replication edit instead.
func (t RemoteTarget) PeerOwned() bool {
	return t.PeerDeploymentID() != ""
}

// replicationARNPeer returns the peer segment of a site-replication owned
// replication target ARN, or "" for any other ARN. RustFS derives those ARNs
// from the topology with the legacy rustfs partition:
// arn:rustfs:replication:<region>:<peer>:<bucket>. Ordinary targets are minted
// under the minio partition, and a peer target that records its deployment id
// is recognised by that field first.
func replicationARNPeer(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) != 6 || parts[0] != "arn" || parts[1] != "rustfs" || parts[2] != "replication" {
		return ""
	}
	return parts[4]
}

// AddRemoteTarget registers (or, when the ARN is set, updates) a remote target
// on the given source bucket and returns the server-assigned ARN.
func (c *RustfsAdmin) AddRemoteTarget(ctx context.Context, bucket string, target RemoteTarget) (string, error) {
	body, err := json.Marshal(target)
	if err != nil {
		return "", err
	}
	query := url.Values{"bucket": []string{bucket}}
	// When the caller knows the ARN it is an update: the server only applies a
	// create-path PUT when the target does not already exist (it returns the
	// existing ARN otherwise). To actually persist the new field values the
	// request must be an explicit `update=true` with the `creds` operation group.
	if target.Arn != "" {
		query.Set("update", "true")
		query.Set("creds", "true")
	}
	reqData := RequestData{
		Method:      "PUT",
		RelPath:     "set-remote-target",
		QueryValues: query,
		Content:     body,
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return "", err
	}
	defer drainClose(resp)
	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return strings.Trim(string(bytes), "\"\n"), nil
}

// ListRemoteTargets returns the remote targets registered on the given source
// bucket.
func (c *RustfsAdmin) ListRemoteTargets(ctx context.Context, bucket string) ([]RemoteTarget, error) {
	reqData := RequestData{
		Method:      "GET",
		RelPath:     "list-remote-targets",
		QueryValues: url.Values{"bucket": []string{bucket}},
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return nil, err
	}
	defer drainClose(resp)
	var targets []RemoteTarget
	err = json.NewDecoder(resp.Body).Decode(&targets)
	return targets, err
}

// DeleteRemoteTarget removes the remote target identified by arn from the given
// source bucket.
func (c *RustfsAdmin) DeleteRemoteTarget(ctx context.Context, bucket, arn string) error {
	reqData := RequestData{
		Method:      "DELETE",
		RelPath:     "remove-remote-target",
		QueryValues: url.Values{"bucket": []string{bucket}, "arn": []string{arn}},
	}
	resp, err := c.doRequest(ctx, reqData)
	if err != nil {
		return err
	}
	defer drainClose(resp)
	return nil
}
