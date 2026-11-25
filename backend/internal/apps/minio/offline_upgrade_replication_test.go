package minio

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

func TestSiloUpgradeInventoryUsesAllActualBucketsAndRejectsFilteredRules(t *testing.T) {
	meta, err := parseOfflineSiloMember(offlineUpgradeInstance("one", "two"), false)
	if err != nil {
		t.Fatal(err)
	}
	raw := offlineUpgradeReplicationOutput("one", "two")
	raw = strings.Replace(raw, "AIFAR_SILO_BUCKET_INVENTORY_BEGIN\n", "AIFAR_SILO_BUCKET_INVENTORY_BEGIN\n"+
		`{"status":"success","type":"folder","key":"other/"}`+"\n", 1)
	state, err := parseOfflineSiloReplication(raw, meta)
	if err != nil || len(state) < 3 || state[2] != `["aifar","other"]` {
		t.Fatalf("all inventory not retained in state: %v, %v", state, err)
	}
	for _, invalid := range []string{
		strings.Replace(raw, `"key":"aifar/"`, `"key":"other/"`, 1),
		strings.Replace(raw, "AIFAR_SILO_BUCKET_INVENTORY_END", "mismatched-end", 1),
		strings.Replace(raw, `"Destination":{"Bucket":"arn:rule"}`, `"Destination":{"Bucket":"arn:rule"},"Filter":{"Prefix":"customer/"}`, 1),
	} {
		if _, err := parseOfflineSiloReplication(invalid, meta); err == nil {
			t.Fatal("accepted a missing/duplicate managed bucket, incomplete inventory, or unsupported filter")
		}
	}
}

type siloCanaryTestRemote struct {
	nodes      map[string]*siloCanaryTestObject
	calls      []string
	noReplica  bool
	occupied   bool
	wrongHash  bool
	configData map[string]string
}

type siloCanaryTestObject struct {
	source, payload string
	removed         map[string]bool
}

const siloCanaryVersion = "8661a8a6-209e-43a8-ae62-6179570407a8"

var siloCanaryTestKey = regexp.MustCompile(`aifar-offline-upgrade/test-task/[0-9a-f]{32}\.probe`)
var siloCanaryTestPayload = regexp.MustCompile(`'[0-9a-f]{64}'`)

func (r *siloCanaryTestRemote) Run(_ context.Context, node store.Server, command string) (adapter.CommandResult, error) {
	phase := strings.TrimPrefix(strings.SplitN(command, "\n", 2)[0], "# aifar-silo-probe ")
	r.calls = append(r.calls, phase+":"+node.ID)
	key := siloCanaryTestKey.FindString(command)
	object := r.nodes[key]
	switch phase {
	case "inventory-rule", "retention":
		return adapter.CommandResult{Stdout: r.configData[phase]}, nil
	case "put":
		payload := strings.Trim(siloCanaryTestPayload.FindString(command), "'")
		if key == "" || payload == "" || object != nil {
			return adapter.CommandResult{}, fmt.Errorf("invalid test PUT")
		}
		r.nodes[key] = &siloCanaryTestObject{source: node.ID, payload: payload, removed: map[string]bool{}}
	case "list":
		if r.occupied || (object != nil && !object.removed[node.ID] && (!r.noReplica || object.source == node.ID)) {
			return adapter.CommandResult{Stdout: fmt.Sprintf(`{"status":"success","type":"file","key":%q,"versionId":%q,"isDeleteMarker":false,"url":%q}`,
				strings.TrimPrefix(key, "aifar-offline-upgrade/test-task/"), siloCanaryVersion, "http://127.0.0.1:9000/aifar/"+key)}, nil
		}
	case "stat":
		return adapter.CommandResult{Stdout: fmt.Sprintf(`{"status":"success","versionID":%q,"replicationStatus":"REPLICA"}`, siloCanaryVersion)}, nil
	case "hash":
		if object != nil {
			if r.wrongHash && object.source != node.ID {
				return adapter.CommandResult{Stdout: "wrong hash"}, nil
			}
			return adapter.CommandResult{Stdout: fmt.Sprintf("%x\n", sha256.Sum256([]byte(object.payload)))}, nil
		}
	case "remove":
		if object == nil || !strings.Contains(command, siloCanaryVersion) {
			return adapter.CommandResult{}, fmt.Errorf("cleanup did not address the known exact version")
		}
		object.removed[node.ID] = true
	}
	return adapter.CommandResult{}, nil
}

func (*siloCanaryTestRemote) UploadFile(context.Context, store.Server, string, string, os.FileMode) error {
	return fmt.Errorf("unexpected upload")
}

func TestSiloCanaryWritesBothDirectionsAndDeletesOnlyVerifiedVersions(t *testing.T) {
	remote := &siloCanaryTestRemote{nodes: map[string]*siloCanaryTestObject{}}
	s := Service{remote: remote}
	nodes := makeSiloCanaryTestNodes(t)
	if err := s.verifyOfflineSiloReplication(context.Background(), nodes, "test-task", "en"); err != nil {
		t.Fatal(err)
	}
	if len(remote.nodes) != 2 {
		t.Fatalf("want two distinct keys, got %+v", remote.nodes)
	}
	var sources []string
	for _, object := range remote.nodes {
		sources = append(sources, object.source)
		if !object.removed["one"] || !object.removed["two"] {
			t.Fatalf("did not clean both observed exact versions: %+v", object.removed)
		}
	}
	slices.Sort(sources)
	if !slices.Equal(sources, []string{"one", "two"}) {
		t.Fatalf("invalid replication directions %v", sources)
	}
}

func TestSiloCanaryRequiresLocalPeerListBeforeMatchingGET(t *testing.T) {
	remote := &siloCanaryTestRemote{nodes: map[string]*siloCanaryTestObject{}, noReplica: true}
	s := Service{remote: remote}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := s.verifyOfflineSiloReplication(ctx, makeSiloCanaryTestNodes(t), "test-task", "en"); err == nil {
		t.Fatal("the proxy GET would match, but peer never stored a replica")
	}
	if slices.Contains(remote.calls, "hash:two") || slices.Contains(remote.calls, "remove:two") {
		t.Fatalf("attempted a proxy GET or delete without the direct version listing: %v", remote.calls)
	}
}

func TestSiloCanaryLeavesExistingAndUnverifiedVersionsUntouched(t *testing.T) {
	for _, remote := range []*siloCanaryTestRemote{
		{nodes: map[string]*siloCanaryTestObject{}, occupied: true},
		{nodes: map[string]*siloCanaryTestObject{}, wrongHash: true},
	} {
		s := Service{remote: remote}
		if err := s.verifyOfflineSiloReplication(context.Background(), makeSiloCanaryTestNodes(t), "test-task", "en"); err == nil {
			t.Fatal("an occupied key or mismatched hash must block the probe")
		}
		for _, call := range remote.calls {
			if strings.HasPrefix(call, "remove:") || (remote.occupied && strings.HasPrefix(call, "put:")) {
				t.Fatalf("an existing or unverified version was changed: %v", remote.calls)
			}
		}
	}
}

func TestSiloInventoryBlocksExtraReplicationAndManagedRetention(t *testing.T) {
	const noReplication = `{"status":"error","error":{"message":"Unable to list replication configuration","cause":{"message":"replication configuration not set","error":{}},"type":"fatal"}}`
	const noLock = `{"status":"error","error":{"message":"Remote bucket ` + "`%s`" + ` does not support locking","cause":{"message":"","error":{}},"type":"fatal"}}`
	state := []string{"one", "two", `["aifar","extra"]`}
	remote := &siloCanaryTestRemote{configData: map[string]string{"inventory-rule": noReplication, "retention": noLock}}
	s := Service{remote: remote}
	node := makeSiloCanaryTestNodes(t)[0]
	if err := s.validateOfflineSiloInventory(context.Background(), node, state, "en"); err != nil {
		t.Fatalf("healthy managed bucket and unreplicated extra were rejected: %v", err)
	}
	remote.configData["inventory-rule"] = `{"status":"success","rule":{"Status":"Enabled"}}`
	if err := s.validateOfflineSiloInventory(context.Background(), node, state, "en"); err == nil || !strings.Contains(err.Error(), "extra") {
		t.Fatalf("extra replication was accepted: %v", err)
	}
	remote.configData["retention"] = `{"status":"success","mode":"COMPLIANCE"}`
	if err := s.validateOfflineSiloInventory(context.Background(), node, state, "en"); err == nil || !strings.Contains(err.Error(), "aifar") {
		t.Fatalf("managed bucket retention was accepted: %v", err)
	}
}

func makeSiloCanaryTestNodes(t *testing.T) []offlineSiloNode {
	t.Helper()
	var nodes []offlineSiloNode
	for _, pair := range [][2]string{{"one", "two"}, {"two", "one"}} {
		meta, err := parseOfflineSiloMember(offlineUpgradeInstance(pair[0], pair[1]), false)
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, offlineSiloNode{server: store.Server{ID: pair[0]}, root: "/aifar/minio", meta: meta})
	}
	return nodes
}
