# MinIO Distributed Templates

MinIO distributed deployment is intentionally separated from the standalone scripts.

The production implementation should add per-node service templates and distributed volume bootstrap templates here. Go remains responsible for node ordering, quorum validation, credentials, task logs, retries, and audit records.
