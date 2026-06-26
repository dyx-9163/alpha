# Redis Cluster Templates

Redis Cluster deployment is intentionally separated from the standalone scripts.

The production implementation should add per-node installation and cluster creation templates here, while Go remains responsible for orchestration, validation, task logs, retries, and audit records.
