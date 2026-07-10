# Scheme 0 Closeout Design

## Goal

Close out the current dirty working tree without discarding any existing work. The result must be reviewable, fail closed for production security and release errors, retain all existing HTTP/UI/task behavior, and leave the branch with independently verified commits.

## Chosen approach

Use **preserve and partition**:

- Treat every currently tracked and untracked change as intentional input.
- Work in the current checkout; do not reset, checkout, stash, or create a detached worktree.
- Stage exact paths or hunks and commit one coherent behavior domain at a time.
- Add characterization tests for existing changes and strict RED/GREEN tests for newly discovered blockers.
- Review every task before starting the next task, then run a whole-branch review.

Rejected alternatives:

- **Reset and reapply:** risks losing user changes and invalidates the current tested baseline.
- **Single squash:** makes concurrency, security, frontend, and release behavior impossible to review independently.

## Behavioral decisions

### Worker

- A recovered job panic ends as `failed`.
- An accepted cancellation cannot later end as `success`.
- A panic wins over a concurrent cancellation and remains `failed`; the cancel endpoint's boolean only reports whether the request was accepted.
- Each terminal outcome emits one `task.updated` and one `task.finished` event.
- Existing task status strings, API response shapes, operation-lock behavior, and deployment concurrency remain unchanged.

### Security and credential encryption

- Insecure defaults are allowed only for `127.0.0.1`, `localhost`, or `::1` listeners.
- Empty hosts, wildcard addresses, and ordinary hostnames are not loopback.
- Explicit `AIFAR_ALLOW_INSECURE_DEFAULTS=false` is never overridden by development scripts.
- Structural rotation errors, including equal current and previous secrets, are rejected even in local insecure mode.
- Before HTTP starts, every known `enc:v1:` value must be decryptable by the configured current key or be atomically rotated using the provided previous key.
- Rotation never changes schema or ciphertext format, never logs secrets, and rolls back all writes on any error.

### Realtime and Containers frontend

- Status cache is last-known state. Failed GETs, empty GETs, and absent keys do not delete existing cache entries.
- Updates for the same key are accepted by `version`, then `collectedAt`, then `updatedAt`.
- Tombstones, TTL, and backend snapshot GC are deferred.
- `ContainersView.vue` remains the page orchestrator. The existing workspace, six runtime tabs, dialogs, API helpers, rules, provider, and typed context are the closeout boundary.
- No component-test framework is added in this batch.

### Startup, tooling, and release

- Runtime defaults accept only `AIFAR_[A-Z0-9_]+=value`, reject malformed or duplicate keys, and use variable existence rather than truthiness for environment precedence.
- Shell sourcing or arbitrary evaluation of the defaults file is forbidden.
- Release security validation happens before copying large resources.
- Staging is always cleaned, archive creation failures are fatal, and verification requires both current-version Linux and Windows directories and archives.
- Verification extracts each archive and checks the complete internal checksum manifest.
- Existing runtime layout, binary names, archive names, and package commands remain stable.

## Scope boundaries

No `/api/v2`, WebSocket, SSE, task, audit, database-schema, migration, installation-topology, permission, or UI behavior changes are allowed. This batch excludes persistent Worker jobs, lease/retry semantics, Runtime/Nacos backend domain extraction, Database/Nacos/Storage frontend refactors, Docker Go client work, real MinIO operations, signing, SBOM, provenance, multi-architecture builds, and real-server mutating E2E.

## Verification strategy

- Focused tests follow TDD for every newly fixed behavior.
- Existing uncommitted user work is characterized before being changed; it is not deleted merely to manufacture a RED state.
- Every task produces one independently reviewable commit and receives spec plus quality review.
- Fast gates cover backend, frontend, scripts, and both builds.
- Ubuntu CI owns race-detector coverage because the local toolchain has `CGO_ENABLED=0`.
- One final `pnpm test:local` run performs the 4.6 GB packaging path and archive verification.

