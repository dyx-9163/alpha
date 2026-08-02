# Runtime Release Deletion Design

## Goal

Allow operators to delete an old AIFAR Runtime release record when no running service uses it, while keeping backend deletion checks authoritative and showing the same eligibility in the UI before the user clicks Delete.

## Current Problem

- Every successful rollout writes `baseReleaseId` as historical ancestry.
- Manual deletion rejects any row referenced by another row's `baseReleaseId` or `rollbackTo`.
- Automatic retention recursively protects the complete `baseReleaseId` chain.
- The frontend only disables pending/running rows, so it offers actions the backend will always reject.
- A partial rollout can make the coarse `baseReleaseId` differ from the actual per-service predecessors in `serviceRevisionsBefore`, causing unnecessary protection.

## Chosen Semantics

`baseReleaseId`, `previousRevision`, `rollbackFrom`, and `rollbackTo` are immutable audit facts, not hard foreign keys. A historical reference may name a release record that has since been pruned. Rollback execution already uses the selected target release's own manifest and artifacts; it does not traverse `baseReleaseId`.

A release record is not deletable only when:

1. its status is `pending` or `running`; or
2. its release ID is currently used by the instance's global revision fields or any `serviceRevisions` entry.

Deletion continues to remove only control-plane records and associated indexes. It does not remove remote artifacts, containers, or runtime state. The DELETE endpoint recalculates eligibility immediately before mutation.

## API and UI

Each release list item includes:

- `deleteAvailable: boolean`
- `deleteUnavailableReason: "" | "AIFAR_RELEASE_DELETE_CURRENT" | "AIFAR_RELEASE_DELETE_ACTIVE"`
- `deleteUnavailableDetails: object`

The frontend disables Delete when the backend reports an unavailable reason and shows a localized tooltip. Existing role, missing-ID, and loading checks remain client-side defenses.

## Retention

Automatic retention keeps the newest three successful records plus any older record still used by a current service revision. It no longer recursively preserves historical `baseReleaseId` ancestors. This makes the configured retention count effective without deleting an active per-service revision.

## Verification

- Backend unit tests cover active service revisions, historical base/rollback references, list eligibility, delete revalidation, and retention with a linear ancestry chain.
- Frontend tests cover backend-reported current/active deletion restrictions and an eligible historical row.
- Run targeted backend and frontend tests, then the full affected backend package, full web test suite, and production web build.
