package redis

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type sentinelRoleSelection struct {
	MasterID    string
	ReplicaIDs  []string
	SentinelIDs []string
	AllIDs      []string
	Explicit    bool
}

func redisPassword(params map[string]any, fallback string) string {
	value, ok := params["password"]
	if !ok {
		value, ok = params["redisPassword"]
	}
	if !ok {
		return strings.TrimSpace(fallback)
	}
	password := strings.TrimSpace(fmt.Sprint(value))
	if password == "" {
		password = strings.TrimSpace(fallback)
	}
	return password
}

func targetServerIDs(req InstallRequest) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	add(req.ServerID)
	for _, id := range req.ServerIDs {
		add(id)
	}
	return out
}

func (r sentinelRoleSelection) IsSentinel(target string) bool {
	return stringInSlice(target, r.SentinelIDs)
}

func (r sentinelRoleSelection) IsReplica(target string) bool {
	return stringInSlice(target, r.ReplicaIDs)
}

func (r sentinelRoleSelection) RoleFor(target string) string {
	switch {
	case target == r.MasterID:
		return "master"
	case r.IsReplica(target):
		return "replica"
	default:
		return "sentinel"
	}
}

func redisSentinelRoles(params map[string]any, legacyTargets []string, copy Copy) (sentinelRoleSelection, error) {
	master := redisSentinelMasterParam(params)
	replicas, hasReplicas := stringSliceParam(params, "replicaServerIds", "replicaIds")
	sentinels, hasSentinels := stringSliceParam(params, "sentinelServerIds", "sentinelIds")
	explicit := hasReplicas || hasSentinels
	if !explicit {
		if len(legacyTargets) < 3 {
			return sentinelRoleSelection{}, errors.New(copy.SentinelNeedNodes)
		}
		selected, err := redisSentinelMasterID(params, legacyTargets, copy)
		if err != nil {
			return sentinelRoleSelection{}, err
		}
		for _, target := range legacyTargets {
			if target != selected {
				replicas = append(replicas, target)
			}
		}
		return sentinelRoleSelection{
			MasterID:    selected,
			ReplicaIDs:  replicas,
			SentinelIDs: append([]string{}, legacyTargets...),
			AllIDs:      append([]string{}, legacyTargets...),
		}, nil
	}
	if master == "" {
		return sentinelRoleSelection{}, errors.New(copy.SentinelMasterRequired)
	}
	if len(replicas) == 0 {
		return sentinelRoleSelection{}, errors.New(copy.SentinelReplicaRequired)
	}
	if stringInSlice(master, replicas) {
		return sentinelRoleSelection{}, errors.New(copy.SentinelReplicaHasMaster)
	}
	if len(sentinels) < 3 {
		return sentinelRoleSelection{}, errors.New(copy.SentinelNodesRequired)
	}
	all := uniqueStrings(append(append([]string{master}, replicas...), sentinels...))
	return sentinelRoleSelection{
		MasterID:    master,
		ReplicaIDs:  replicas,
		SentinelIDs: sentinels,
		AllIDs:      all,
		Explicit:    true,
	}, nil
}

func redisPort(params map[string]any) int {
	value, ok := params["port"]
	if !ok {
		return 6379
	}
	switch v := value.(type) {
	case int:
		return normalizePort(v)
	case int64:
		return normalizePort(int(v))
	case float64:
		return normalizePort(int(v))
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return normalizePort(n)
	default:
		return 6379
	}
}

func redisSentinelPort(params map[string]any) int {
	return intParam(params, "sentinelPort", 26379)
}

func redisQuorum(params map[string]any, targetCount int) int {
	defaultQuorum := targetCount/2 + 1
	if defaultQuorum < 2 {
		defaultQuorum = 2
	}
	return intParam(params, "quorum", defaultQuorum)
}

func redisSentinelMasterName(params map[string]any, invalidMessage string) (string, error) {
	name := strings.TrimSpace(fmt.Sprint(params["masterName"]))
	if name == "" || name == "<nil>" {
		name = strings.TrimSpace(fmt.Sprint(params["sentinelMasterName"]))
	}
	if name == "" || name == "<nil>" {
		return "aifar-master", nil
	}
	if len(name) > 64 {
		return "", errors.New(invalidMessage)
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return "", errors.New(invalidMessage)
	}
	return name, nil
}

func redisSentinelMasterID(params map[string]any, targets []string, copy Copy) (string, error) {
	if len(targets) == 0 {
		return "", errors.New(copy.SentinelMasterRequired)
	}
	selected := redisSentinelMasterParam(params)
	if selected == "" {
		return "", errors.New(copy.SentinelMasterRequired)
	}
	for _, target := range targets {
		if target == selected {
			return selected, nil
		}
	}
	return "", errors.New(copy.SentinelMasterNotSelected)
}

func redisSentinelMasterParam(params map[string]any) string {
	if params == nil {
		return ""
	}
	for _, key := range []string{"sentinelMasterId", "masterServerId"} {
		selected := strings.TrimSpace(fmt.Sprint(params[key]))
		if selected != "" && selected != "<nil>" {
			return selected
		}
	}
	return ""
}

func stringSliceParam(params map[string]any, keys ...string) ([]string, bool) {
	if params == nil {
		return nil, false
	}
	for _, key := range keys {
		value, ok := params[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case []string:
			return uniqueStrings(v), true
		case []any:
			out := make([]string, 0, len(v))
			for _, item := range v {
				out = append(out, fmt.Sprint(item))
			}
			return uniqueStrings(out), true
		case string:
			return uniqueStrings(strings.Split(v, ",")), true
		default:
			return uniqueStrings([]string{fmt.Sprint(v)}), true
		}
	}
	return nil, false
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "<nil>" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func stringInSlice(value string, values []string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func redisClusterReplicas(params map[string]any) int {
	replicas := intParam(params, "replicas", 0)
	if replicas < 0 {
		return 0
	}
	return replicas
}

func intParam(params map[string]any, key string, fallback int) int {
	value, ok := params[key]
	if !ok {
		return fallback
	}
	switch v := value.(type) {
	case int:
		return normalizePortWithFallback(v, fallback)
	case int64:
		return normalizePortWithFallback(int(v), fallback)
	case float64:
		return normalizePortWithFallback(int(v), fallback)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return normalizePortWithFallback(n, fallback)
	default:
		return fallback
	}
}

func normalizePort(port int) int {
	if port <= 0 || port > 65535 {
		return 6379
	}
	return port
}

func normalizePortWithFallback(port, fallback int) int {
	if port <= 0 || port > 65535 {
		return fallback
	}
	return port
}
