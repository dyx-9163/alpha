package minio

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"slices"
	"strings"
	"time"
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
	decoder := json.NewDecoder(strings.NewReader(raw))
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

	for _, bucket := range meta.Buckets {
		response, err := read()
		if err != nil || response.URL != "aifar-local/"+bucket {
			return nil, errInvalidSiloReplication
		}
		versionState, err := canonicalSiloObject(response.Versioning)
		if err != nil || versionState["status"] != "Enabled" {
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
