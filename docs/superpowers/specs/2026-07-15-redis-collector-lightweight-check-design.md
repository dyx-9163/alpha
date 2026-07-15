# Redis Collector Lightweight Check Design

## Problem

Redis Sentinel instances are healthy, but the database page reports every Redis data node as offline. The five-second per-instance collector deadline expires while the Redis check performs several sequential SSH operations. The collector then stores an `unavailable` snapshot even though the full check finishes shortly afterward and updates the instance to `running`.

## Scope

Only the background collector path changes. Manual Redis checks keep their existing complete behavior, including service-access reconciliation, Sentinel topology discovery, endpoint probing, role detection, instance metadata updates, task steps, and logs.

## Design

When `CheckRequest.Actor` is `collector`, the Redis module will run one lightweight, read-only remote command against the instance server. The command will:

- resolve the current or legacy Redis install root;
- check the expected Redis data and Sentinel systemd services;
- authenticate with the instance-bound Redis credential;
- execute Redis and Sentinel `PING` checks where applicable;
- query the local Redis data role when the instance contains a data node;
- return a compact, machine-readable result.

The collector path will not change firewall or SELinux state, discover the complete Sentinel topology, or probe peer endpoints. It will update only the checked instance's local runtime status and role using the command result. Existing topology metadata remains available for grouping and display.

All non-collector callers continue through the current full check path unchanged.

## Error Handling

Authentication, SSH, systemd, Redis, and parsing failures return a failed check result through the existing status-update path. Context cancellation and the collector's five-second deadline remain authoritative. No timeout increase is introduced.

## Tests

- A collector Redis Sentinel check performs exactly one remote command and does not execute service-access or Sentinel topology commands.
- The collector command uses the bound Redis credential instead of the panel default password.
- The collector result updates data and Sentinel component status and the local Redis role.
- A non-collector manual check still runs the existing full Sentinel workflow.

## Acceptance Criteria

- Healthy Redis Sentinel instances complete collector checks within five seconds under normal SSH latency.
- Collector snapshots become `running` instead of `unavailable` for the reported environment.
- Manual detection retains full topology reconciliation.
- Redis, collector, and full backend tests pass.
