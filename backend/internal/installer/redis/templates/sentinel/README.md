# Redis Sentinel Templates

Redis Sentinel deployment is intentionally separated from the standalone scripts.

The production implementation should add node installation and Sentinel bootstrap templates here, while Go remains responsible for target selection, execution order, task logs, retries, and audit records.
