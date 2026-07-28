SELECT @@version AS mysql_version, @@server_uuid AS server_uuid, @@GLOBAL.gtid_executed AS gtid_executed;
SELECT schema_name
FROM information_schema.schemata
WHERE schema_name NOT IN ('information_schema', 'mysql', 'mysql_innodb_cluster_metadata', 'performance_schema', 'sys')
ORDER BY schema_name;
