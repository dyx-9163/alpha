package minio

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
)

//go:embed templates/upgrade/replication-probe.sh
var offlineSiloReplicationProbeScript string

const (
	siloInventoryBegin = "AIFAR_SILO_BUCKET_INVENTORY_BEGIN\n"
	siloInventoryEnd   = "AIFAR_SILO_BUCKET_INVENTORY_END\n"
)

type offlineReplicaTarget struct {
	ARN          string    `json:"arn"`
	Endpoint     string    `json:"endpoint"`
	SourceBucket string    `json:"sourcebucket"`
	TargetBucket string    `json:"targetbucket"`
	Secure       bool      `json:"secure"`
	Online       bool      `json:"isOnline"`
	LastOnline   time.Time `json:"lastOnline"`
	Type         string    `json:"type"`
	Path         string    `json:"path"`
	API          string    `json:"api"`
	Region       string    `json:"region"`
	Bandwidth    int64     `json:"bandwidthlimit"`
	Sync         bool      `json:"replicationSync"`
	DisableProxy bool      `json:"disableProxy"`
}

type offlineReplicationCounts struct {
	Count float64 `json:"count"`
	Bytes float64 `json:"bytes"`
}

func (c offlineReplicationCounts) empty() bool {
	return c.Count == 0 && c.Bytes == 0
}

type offlineReplicationQueue struct {
	Current *offlineReplicationCounts `json:"curr"`
}

type offlineReplicaResponse struct {
	Status     string          `json:"status"`
	URL        string          `json:"url"`
	Rule       json.RawMessage `json:"rule"`
	Versioning json.RawMessage `json:"versioning"`
	Info       *struct {
		DeploymentID string `json:"deploymentID"`
		Servers      []struct {
			State  string `json:"state"`
			Drives []struct {
				State   string `json:"state"`
				Healing bool   `json:"healing"`
			} `json:"drives"`
		} `json:"servers"`
	} `json:"info"`
	Targets []offlineReplicaTarget `json:"remoteTargets"`
	Stats   *struct {
		Bucket struct {
			Queued       offlineReplicationQueue `json:"queued"`
			InProgress   offlineReplicationQueue `json:"inProgress"`
			PendingSize  uint64                  `json:"pendingReplicationSize"`
			PendingCount uint64                  `json:"pendingReplicationCount"`
			FailedSize   uint64                  `json:"failedReplicationSize"`
			FailedCount  uint64                  `json:"failedReplicationCount"`
			Failed       struct {
				LastMinute offlineReplicationCounts `json:"lastMinute"`
			} `json:"failed"`
		} `json:"currStats"`
		Queue struct {
			Nodes []struct {
				Queued     offlineReplicationQueue `json:"queueStats"`
				InProgress offlineReplicationQueue `json:"progressStats"`
				MRF        struct {
					RecentlyFailed uint64 `json:"failedCount_last5min"`
					Dropped        uint64 `json:"droppedCount_since_uptime"`
				} `json:"mrfStats"`
			} `json:"nodes"`
		} `json:"queueStats"`
	} `json:"replicationstats"`
}

var errInvalidSiloReplication = errors.New("invalid MinIO replication inspection")

func parseOfflineSiloReplication(raw string, meta offlineSiloMetadata) ([]string, error) {
	peer, err := url.Parse(meta.Peer)
	if err != nil {
		return nil, errInvalidSiloReplication
	}
	header, rest, found := strings.Cut(raw, siloInventoryBegin)
	if !found || !strings.HasSuffix(header, "\n") {
		return nil, errInvalidSiloReplication
	}
	inventoryRaw, rest, found := strings.Cut(rest, siloInventoryEnd)
	if !found || !strings.HasSuffix(inventoryRaw, "\n") {
		return nil, errInvalidSiloReplication
	}
	inventory, err := parseOfflineSiloBucketInventory(inventoryRaw, meta.Buckets)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(header))
	read := func() (offlineReplicaResponse, error) {
		var response offlineReplicaResponse
		if err := decoder.Decode(&response); err != nil || response.Status != "success" {
			return response, errInvalidSiloReplication
		}
		return response, nil
	}
	var state []string
	for i := 0; i < 2; i++ {
		response, err := read()
		if err != nil || response.Info == nil || response.Info.DeploymentID == "" || len(response.Info.Servers) != 1 {
			return nil, errInvalidSiloReplication
		}
		server := response.Info.Servers[0]
		if server.State != "online" || len(server.Drives) == 0 ||
			(i == 0 && len(server.Drives) != len(meta.DataDirs)) {
			return nil, errInvalidSiloReplication
		}
		for _, drive := range server.Drives {
			if drive.State != "ok" || drive.Healing {
				return nil, errInvalidSiloReplication
			}
		}
		state = append(state, response.Info.DeploymentID)
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, errInvalidSiloReplication
	}
	state = append(state, inventory)
	decoder = json.NewDecoder(strings.NewReader(rest))

	for _, bucket := range meta.Buckets {
		response, err := read()
		if err != nil || response.URL != "aifar-local/"+bucket {
			return nil, errInvalidSiloReplication
		}
		versionState, err := canonicalSiloObject(response.Versioning)
		if err != nil || versionState["status"] != "Enabled" || hasSiloVersionExclusions(versionState) {
			return nil, errInvalidSiloReplication
		}
		version, _ := json.Marshal(versionState)
		state = append(state, bucket, string(version))
		var rules []string
		enabledTargets := map[string]struct{}{}
		for {
			response, err = read()
			if err != nil {
				return nil, errInvalidSiloReplication
			}
			if response.Stats != nil {
				if response.URL != "aifar-local/"+bucket {
					return nil, errInvalidSiloReplication
				}
				break
			}
			rule, err := canonicalSiloObject(response.Rule)
			if err != nil {
				return nil, errInvalidSiloReplication
			}
			destination, _ := rule["Destination"].(map[string]any)
			arn, _ := destination["Bucket"].(string)
			status, _ := rule["Status"].(string)
			if arn == "" || (status != "Enabled" && status != "Disabled") {
				return nil, errInvalidSiloReplication
			}
			if status == "Enabled" {
				if !emptySiloRuleFilter(rule["Prefix"]) || !emptySiloRuleFilter(rule["Filter"]) {
					return nil, errInvalidSiloReplication
				}
				enabledTargets[arn] = struct{}{}
			}
			encoded, _ := json.Marshal(rule)
			rules = append(rules, string(encoded))
		}
		if len(enabledTargets) == 0 || !response.queueDrained() {
			return nil, errInvalidSiloReplication
		}
		var routes []string
		for _, target := range response.Targets {
			if _, ok := enabledTargets[target.ARN]; !ok {
				continue
			}
			// MinIO also reports false before its first heartbeat sample is recorded.
			// The shell already checks the peer directly and authenticates its admin API.
			if (!target.Online && !target.LastOnline.IsZero()) || target.Type != "replication" ||
				target.SourceBucket != bucket || target.TargetBucket != bucket ||
				!strings.EqualFold(target.Endpoint, peer.Host) || target.Secure != (peer.Scheme == "https") {
				return nil, errInvalidSiloReplication
			}
			target.Online, target.LastOnline = false, time.Time{}
			encoded, _ := json.Marshal(target)
			routes = append(routes, string(encoded))
			delete(enabledTargets, target.ARN)
		}
		if len(enabledTargets) != 0 {
			return nil, errInvalidSiloReplication
		}
		slices.Sort(rules)
		slices.Sort(routes)
		state = append(state, rules...)
		state = append(state, routes...)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errInvalidSiloReplication
	}
	return state, nil
}

func parseOfflineSiloBucketInventory(raw string, expected []string) (string, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	var buckets []string
	for {
		var entry struct{ Status, Type, Key string }
		err := decoder.Decode(&entry)
		if err == io.EOF {
			break
		}
		bucket := strings.TrimSuffix(entry.Key, "/")
		if err != nil || entry.Status != "success" || entry.Type != "folder" || bucket == entry.Key ||
			validateMinioBucketName(bucket) != nil || slices.Contains(buckets, bucket) {
			return "", errInvalidSiloReplication
		}
		buckets = append(buckets, bucket)
	}
	for _, bucket := range expected {
		if !slices.Contains(buckets, bucket) {
			return "", errInvalidSiloReplication
		}
	}
	slices.Sort(buckets)
	inventory, err := json.Marshal(buckets)
	return string(inventory), err
}

func emptySiloRuleFilter(value any) bool {
	switch value := value.(type) {
	case nil:
		return true
	case string:
		return value == ""
	case map[string]any:
		for _, child := range value {
			if !emptySiloRuleFilter(child) {
				return false
			}
		}
		return true
	case []any:
		return len(value) == 0
	default:
		return false
	}
}

func hasSiloVersionExclusions(versioning map[string]any) bool {
	for name, value := range versioning {
		switch strings.ToLower(name) {
		case "excludedprefixes":
			if !emptySiloRuleFilter(value) {
				return true
			}
		case "excludefolders":
			if value != false && value != nil {
				return true
			}
		}
	}
	return false
}

func (r offlineReplicaResponse) queueDrained() bool {
	bucket := r.Stats.Bucket
	if bucket.Queued.Current == nil || !bucket.Queued.Current.empty() ||
		(bucket.InProgress.Current != nil && !bucket.InProgress.Current.empty()) ||
		bucket.PendingCount != 0 || bucket.PendingSize != 0 ||
		bucket.FailedCount != 0 || bucket.FailedSize != 0 || !bucket.Failed.LastMinute.empty() {
		return false
	}
	for _, node := range r.Stats.Queue.Nodes {
		if (node.Queued.Current != nil && !node.Queued.Current.empty()) ||
			(node.InProgress.Current != nil && !node.InProgress.Current.empty()) ||
			node.MRF.RecentlyFailed != 0 || node.MRF.Dropped != 0 {
			return false
		}
	}
	return true
}

func canonicalSiloObject(raw json.RawMessage) (map[string]any, error) {
	var fields map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	err := decoder.Decode(&fields)
	return fields, err
}

const (
	siloProbeTimeout = 45 * time.Second
	siloProbeWait    = 500 * time.Millisecond
)

var siloProbeVersion = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (s Service) validateOfflineSiloInventory(ctx context.Context, node offlineSiloNode, state []string, language string) error {
	var inventory []string
	if len(state) < 3 || json.Unmarshal([]byte(state[2]), &inventory) != nil {
		return errInvalidSiloReplication
	}
	for _, bucket := range inventory {
		managed := slices.Contains(node.meta.Buckets, bucket)
		phase := "inventory-rule"
		if managed {
			phase = "retention"
		}
		output, err := s.runOfflineSiloProbe(ctx, node, phase, bucket, "", "")
		if err == nil && absentSiloProbeConfiguration(output, managed) {
			continue
		}
		if managed {
			return fmt.Errorf(siloText(language,
				"无法确认存储桶 %s 未启用对象锁，已停止复制验证",
				"Could not confirm that bucket %s has no object lock"), bucket)
		}
		return fmt.Errorf(siloText(language,
			"未登记存储桶 %s 已配置复制，或无法确认其复制状态",
			"Unrecorded bucket %s has replication configured or could not be checked"), bucket)
	}
	return nil
}

func absentSiloProbeConfiguration(raw string, managed bool) bool {
	var response struct {
		Status string
		Error  struct {
			Message, Type string
			Cause         struct{ Message string }
		}
	}
	if json.Unmarshal([]byte(raw), &response) != nil || response.Status != "error" || response.Error.Type != "fatal" {
		return false
	}
	if managed {
		return response.Error.Message == "Remote bucket `%s` does not support locking" && response.Error.Cause.Message == ""
	}
	return response.Error.Message == "Unable to list replication configuration" &&
		response.Error.Cause.Message == "replication configuration not set"
}

func (s Service) verifyOfflineSiloReplication(ctx context.Context, nodes []offlineSiloNode, operationID, language string) error {
	if len(nodes) != 2 || !siloTaskIDPattern.MatchString(operationID) ||
		!slices.Equal(nodes[0].meta.Buckets, nodes[1].meta.Buckets) {
		return errInvalidSiloReplication
	}
	for index, source := range nodes {
		for _, bucket := range source.meta.Buckets {
			if err := s.verifyOfflineSiloBucket(ctx, source, nodes[1-index], operationID, bucket, language); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s Service) verifyOfflineSiloBucket(ctx context.Context, source, peer offlineSiloNode, operationID, bucket, language string) error {
	ctx, cancel := context.WithTimeout(ctx, siloProbeTimeout)
	defer cancel()

	var random [48]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	key := fmt.Sprintf("aifar-offline-upgrade/%s/%x.probe", operationID, random[:16])
	payload := fmt.Sprintf("%x", random[16:])
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))
	for _, node := range []offlineSiloNode{source, peer} {
		version, err := s.offlineSiloLocalVersion(ctx, node, bucket, key)
		if err != nil || version != "" {
			return fmt.Errorf(siloText(language,
				"无法确认两端都未占用复制探针键 %s/%s",
				"Could not confirm that replication probe key %s/%s is unused at both sites"), bucket, key)
		}
	}
	// If the status of a PUT or of the replica is ambiguous, never remove
	// anything. Report the reserved key so it can be reconciled explicitly.
	probeFailed := func() error {
		return fmt.Errorf(siloText(language,
			"从 %s 到 %s 的复制探针未完成；测试对象 %s/%s 可能仍保留",
			"Replication probe from %s to %s did not complete. The test object %s/%s may remain"),
			source.meta.Endpoint, peer.meta.Endpoint, bucket, key)
	}
	if _, err := s.runOfflineSiloProbe(ctx, source, "put", bucket, key, payload); err != nil {
		return probeFailed()
	}
	version, err := s.offlineSiloLocalVersion(ctx, source, bucket, key)
	if err != nil || version == "" || !s.offlineSiloProbeHashMatches(ctx, source, bucket, key, version, digest) {
		return probeFailed()
	}
	if err := s.waitForSiloReplica(ctx, peer, bucket, key, version); err != nil ||
		!s.offlineSiloProbeHashMatches(ctx, peer, bucket, key, version, digest) {
		return probeFailed()
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), siloProbeTimeout)
	defer cleanupCancel()
	for _, node := range []offlineSiloNode{source, peer} {
		current, err := s.offlineSiloLocalVersion(cleanupCtx, node, bucket, key)
		if err != nil || (current != "" && current != version) {
			return probeFailed()
		}
		if current == "" {
			// A configured replication delete can already remove the peer.
			continue
		}
		_, _ = s.runOfflineSiloProbe(cleanupCtx, node, "remove", bucket, key, version)
		if current, err = s.offlineSiloLocalVersion(cleanupCtx, node, bucket, key); err != nil || current != "" {
			return probeFailed()
		}
	}
	return nil
}

func (s Service) waitForSiloReplica(ctx context.Context, peer offlineSiloNode, bucket, key, version string) error {
	for {
		local, err := s.offlineSiloLocalVersion(ctx, peer, bucket, key)
		if err != nil || (local != "" && local != version) {
			return errInvalidSiloReplication
		}
		if local == version {
			raw, err := s.runOfflineSiloProbe(ctx, peer, "stat", bucket, key, version)
			var stat struct {
				Status, VersionID, ReplicationStatus string
			}
			if err != nil || json.Unmarshal([]byte(raw), &stat) != nil || stat.Status != "success" ||
				stat.VersionID != version || stat.ReplicationStatus != "REPLICA" {
				return errInvalidSiloReplication
			}
			return nil
		}
		timer := time.NewTimer(siloProbeWait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s Service) offlineSiloLocalVersion(ctx context.Context, node offlineSiloNode, bucket, key string) (string, error) {
	raw, err := s.runOfflineSiloProbe(ctx, node, "list", bucket, key, "")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	var object struct {
		Status, Type, Key, VersionID, URL string
		IsDeleteMarker                    bool
	}
	wantURL := offlineSiloLoopback(node) + "/" + bucket + "/" + key
	if json.Unmarshal([]byte(raw), &object) != nil || object.Status != "success" || object.Type != "file" ||
		object.IsDeleteMarker || object.Key != path.Base(key) || object.URL != wantURL ||
		!siloProbeVersion.MatchString(object.VersionID) {
		return "", errInvalidSiloReplication
	}
	return object.VersionID, nil
}

func (s Service) offlineSiloProbeHashMatches(ctx context.Context, node offlineSiloNode, bucket, key, version, digest string) bool {
	output, err := s.runOfflineSiloProbe(ctx, node, "hash", bucket, key, version)
	return err == nil && strings.TrimSpace(output) == digest
}

func offlineSiloLoopback(node offlineSiloNode) string {
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(node.meta.APIPort))
}

func (s Service) runOfflineSiloProbe(ctx context.Context, node offlineSiloNode, phase, bucket, key, value string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	args := []string{phase, node.root, offlineSiloLoopback(node), bucket, key, value}
	for i := range args {
		args[i] = installerkit.ShellQuote(args[i])
	}
	const delimiter = "AIFAR_SILO_REPLICATION_PROBE"
	command := "# aifar-silo-probe " + phase + "\naifar_sudo=\nif [ \"$(id -u)\" -ne 0 ]; then aifar_sudo='sudo -n'; fi\n" +
		"$aifar_sudo bash -s -- " + strings.Join(args, " ") + " <<'" + delimiter + "'\n" + offlineSiloReplicationProbeScript + "\n" + delimiter
	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := s.remote.Run(deadline, node.server, command)
	if err != nil || result.ExitCode != 0 {
		return "", errInvalidSiloReplication
	}
	return result.Stdout, nil
}
