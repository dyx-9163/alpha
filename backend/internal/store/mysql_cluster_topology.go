package store

import (
	"database/sql"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"
)

var legacyMySQLClusterID = regexp.MustCompile(`^mysql_cluster_[0-9a-f]{24}$`)

type legacyMySQLClusterInstance struct {
	id       string
	serverID string
	metadata map[string]any
}

func backfillLegacyMySQLClusterTopologies(tx *sql.Tx) error {
	rows, err := tx.Query(`select id,coalesce(server_id,''),coalesce(metadata,'{}')
		from app_instances where app='mysql' and topology='innodb-cluster' order by id`)
	if err != nil {
		return err
	}
	groups := map[string][]legacyMySQLClusterInstance{}
	for rows.Next() {
		var id, serverID, rawMetadata string
		if err := rows.Scan(&id, &serverID, &rawMetadata); err != nil {
			rows.Close()
			return err
		}
		metadata := map[string]any{}
		if err := json.Unmarshal([]byte(rawMetadata), &metadata); err != nil {
			continue
		}
		clusterID := strings.TrimSpace(stringMetadataValue(metadata, "clusterId"))
		if !legacyMySQLClusterID.MatchString(clusterID) {
			continue
		}
		groups[clusterID] = append(groups[clusterID], legacyMySQLClusterInstance{id: id, serverID: strings.TrimSpace(serverID), metadata: metadata})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	legacyIDs := make([]string, 0, len(groups))
	for clusterID := range groups {
		legacyIDs = append(legacyIDs, clusterID)
	}
	sort.Strings(legacyIDs)
	for _, legacyID := range legacyIDs {
		if err := backfillOneLegacyMySQLClusterTopology(tx, legacyID, groups[legacyID]); err != nil {
			return err
		}
	}
	return nil
}

func backfillOneLegacyMySQLClusterTopology(tx *sql.Tx, legacyID string, instances []legacyMySQLClusterInstance) error {
	if len(instances) != 3 {
		return nil
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].id < instances[j].id })
	clusterName := ""
	seenServers := map[string]bool{}
	for _, instance := range instances {
		name := strings.TrimSpace(stringMetadataValue(instance.metadata, "clusterName"))
		if name == "" || instance.serverID == "" || seenServers[instance.serverID] || hasMySQLCoordinationMarker(instance.metadata) {
			return nil
		}
		if clusterName == "" {
			clusterName = name
		} else if clusterName != name {
			return nil
		}
		seenServers[instance.serverID] = true
	}
	var existing int
	if err := tx.QueryRow(`select count(*) from app_clusters where app='mysql' and name=?`, clusterName).Scan(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return nil
	}

	routers, err := legacyMySQLClusterRouters(tx, legacyID)
	if err != nil {
		return err
	}
	clusterID := NewID("cluster")
	now := time.Now().UTC()
	clusterMetadata, _ := json.Marshal(map[string]any{"legacyClusterId": legacyID, "source": "legacy-install-backfill"})
	if _, err := tx.Exec(`insert into app_clusters(id,app,name,topology,status,metadata,created_at,updated_at) values(?,?,?,?,?,?,?,?)`,
		clusterID, "mysql", clusterName, "innodb-cluster", "active", string(clusterMetadata), now, now); err != nil {
		return err
	}
	for index, instance := range instances {
		role := "SECONDARY"
		if index == 0 {
			role = "PRIMARY"
		}
		if _, err := tx.Exec(`insert into app_cluster_members(id,cluster_id,instance_id,server_id,role,status,metadata,created_at,updated_at) values(?,?,?,?,?,?,?,?,?)`,
			NewID("clmember"), clusterID, instance.id, instance.serverID, role, "ONLINE", `{}`, now, now); err != nil {
			return err
		}
		instance.metadata["clusterId"] = clusterID
		encoded, err := json.Marshal(instance.metadata)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`update app_instances set metadata=?,updated_at=? where id=?`, string(encoded), now, instance.id); err != nil {
			return err
		}
	}
	for _, router := range routers {
		router.metadata["clusterId"] = clusterID
		encoded, err := json.Marshal(router.metadata)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`update app_instances set metadata=?,updated_at=? where id=?`, string(encoded), now, router.id); err != nil {
			return err
		}
	}
	return nil
}

func legacyMySQLClusterRouters(tx *sql.Tx, legacyID string) ([]legacyMySQLClusterInstance, error) {
	rows, err := tx.Query(`select id,coalesce(server_id,''),coalesce(metadata,'{}') from app_instances where app='mysql-router' order by id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var routers []legacyMySQLClusterInstance
	for rows.Next() {
		var id, serverID, rawMetadata string
		if err := rows.Scan(&id, &serverID, &rawMetadata); err != nil {
			return nil, err
		}
		metadata := map[string]any{}
		if err := json.Unmarshal([]byte(rawMetadata), &metadata); err != nil {
			continue
		}
		if strings.TrimSpace(stringMetadataValue(metadata, "clusterId")) == legacyID {
			routers = append(routers, legacyMySQLClusterInstance{id: id, serverID: serverID, metadata: metadata})
		}
	}
	return routers, rows.Err()
}

func stringMetadataValue(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return value
}

func hasMySQLCoordinationMarker(metadata map[string]any) bool {
	_, maintenance := metadata["mysqlMaintenance"]
	_, reconciliation := metadata["mysqlReconciliation"]
	return maintenance || reconciliation
}
