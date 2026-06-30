# MySQL InnoDB Cluster Templates

MySQL InnoDB Cluster deployment is intentionally separated from the standalone scripts.

The production implementation should add seed-node installation, replica-node installation, router configuration, and cluster bootstrap templates here. Go remains responsible for topology validation, target ordering, task logs, retries, and audit records.
