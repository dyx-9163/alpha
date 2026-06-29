---
name: aifar-deployment-workflow
description: Use for any work in the AIFAR Deployment repository, including feature implementation, debugging, code review, architecture decisions, business module definition, tests, documentation, packaging, and UI changes. Always read memory.md first, follow AGENTS.md, preserve the Go/Vue offline deployment architecture, and append a concise problem/conclusion entry to memory.md before finishing.
---

# AIFAR Deployment Workflow

Use this skill for work inside `D:\workspace\aifar-deployment`.

## Required Start

1. Read `memory.md` first. If it does not exist, create it before finishing.
2. Read `AGENTS.md` for current architecture, code facts, module boundaries, and constraints.
3. Inspect the relevant source files before proposing or editing.
4. If modifying `web/src` layout, visual style, pages, or components, read `design/ant-design-system-portable202606.md` before editing.

## Work Pattern

1. Classify the request:
   - Backend API/service/store/installer/worker
   - Frontend page/component/app registry/i18n
   - Application module lifecycle
   - Resource/package/startup script
   - Documentation or architecture
   - Review/debug/test
2. Keep changes inside the existing module boundary:
   - HTTP handlers stay thin.
   - App Store behavior comes from frontend and backend registries.
   - Remote installation goes through installer services and adapter interfaces.
   - Server workbench logic stays in `backend/internal/servers` and `web/src/servers`.
3. Preserve task and audit behavior for every state-changing action.
4. Preserve zh/en i18n for all user-visible text, backend task logs, and errors.
5. Use fake prober/remote implementations in tests; do not connect to real SSH or Docker endpoints from tests.

## Current Implementation Anchors

- Backend entry: `backend/cmd/aifar-server/main.go`
- Admin CLI: `backend/cmd/aifar-admin/main.go`
- HTTP API: `backend/internal/httpapi/api.go`
- Store: `backend/internal/store`
- Worker: `backend/internal/worker`
- Servers: `backend/internal/servers` and `web/src/servers`
- Resource scanner: `backend/internal/resource`
- App registry: `backend/internal/apps/registry` and `web/src/apps/registry`
- App modules: `backend/internal/apps/{docker,mysql,redis,minio}` and `web/src/apps/{docker,mysql,redis,minio}`
- Installers: `backend/internal/installer/{docker,mysql,redis,minio}`
- Shared install dialog: `web/src/components/AppInstallDialog.vue`
- Task view: `web/src/components/TaskLogPane.vue`
- Design system: `design/ant-design-system-portable202606.md`

## Validation

- Backend: run `pnpm test` or `go test ./...` from `backend/`.
- Frontend: run `pnpm web:build` for TypeScript/Vite validation.
- Full package confidence: run `pnpm build`.
- If validation cannot run, state the reason and residual risk.

## Required Finish

Before the final response, append one concise entry to `memory.md`:

```markdown
## YYYY-MM-DD
- 问题：<用户本轮问题>
- 结论：<完成的决定、实现结果或未完成原因>
```

Keep memory entries short and factual. Never write secrets or bulky logs.
