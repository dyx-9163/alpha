# AIFAR HTTPS Ingress Module Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a self-contained Docker-based HTTPS ingress directory that can be copied to an AIFAR Linux server and controlled with simple shell scripts and an optional systemd unit.

**Architecture:** A dedicated `nginx:stable-alpine` container uses Linux host networking, terminates TLS on ports 80/443, forwards pages to the AIFAR Agent web listener on `127.0.0.1:8080`, and forwards `/api/` plus `/im/ws` to the gateway listener on `127.0.0.1:38000`. Configuration, a bootstrap self-signed certificate for `aifar.local`, and service scripts remain on the host and are mounted read-only. Docker restart policy provides container-level restart; the optional systemd unit provides explicit boot ordering after Docker.

**Tech Stack:** POSIX shell, Docker CLI, Nginx Alpine, OpenSSL-generated X.509 certificate, systemd, Node.js built-in test runner.

## Global Constraints

- The module must not modify or restart existing AIFAR Runtime containers.
- The default domain is `aifar.local`; its bundled certificate is self-signed and must be documented as replaceable for production.
- No production secret or trusted certificate private key may be stored in the repository.
- All mounts are read-only and all runtime state remains inside the copied module directory.
- Linux host networking is required so upstreams remain `127.0.0.1:8080` and `127.0.0.1:38000`.
- Scripts use LF endings and fail fast on missing Docker, certificate, configuration, upstream listener, or occupied HTTPS ports.

---

### Task 1: Module contract tests

**Files:**
- Create: `scripts/https-ingress-module.test.mjs`

**Interfaces:**
- Consumes: repository file tree.
- Produces: assertions for required module files, proxy routes, Docker host networking, restart policy, certificate SAN, and systemd commands.

- [ ] Write tests that require the full module structure and safe operational flags.
- [ ] Run `node --test scripts/https-ingress-module.test.mjs` and confirm failure because `extras/aifar-https-ingress` does not exist.

### Task 2: Deployable module

**Files:**
- Create: `extras/aifar-https-ingress/config.env`
- Create: `extras/aifar-https-ingress/conf.d/aifar.conf`
- Create: `extras/aifar-https-ingress/tls/fullchain.pem`
- Create: `extras/aifar-https-ingress/tls/privkey.pem`
- Create: `extras/aifar-https-ingress/start.sh`
- Create: `extras/aifar-https-ingress/stop.sh`
- Create: `extras/aifar-https-ingress/reload.sh`
- Create: `extras/aifar-https-ingress/status.sh`
- Create: `extras/aifar-https-ingress/install-systemd.sh`
- Create: `extras/aifar-https-ingress/uninstall-systemd.sh`
- Create: `extras/aifar-https-ingress/aifar-https-ingress.service`
- Create: `extras/aifar-https-ingress/README.md`

**Interfaces:**
- Consumes: Docker CLI, existing AIFAR listeners on 8080 and 38000, host ports 80/443.
- Produces: container `aifar-https-ingress`, systemd unit `aifar-https-ingress.service`, HTTPS endpoint for `aifar.local`.

- [ ] Implement Nginx routing with forwarded HTTPS headers and WebSocket upgrade support.
- [ ] Add a self-signed certificate whose SAN contains `DNS:aifar.local`; set the private key to mode 0600.
- [ ] Implement idempotent start, stop, reload, and status scripts.
- [ ] Implement systemd install/uninstall scripts using the copied module's absolute path.
- [ ] Document DNS/hosts setup, certificate replacement, firewall, validation, and rollback.
- [ ] Run the focused test and make it pass.

### Task 3: Verification and handoff

**Files:**
- Modify: `memory.md`

**Interfaces:**
- Consumes: completed module.
- Produces: verified operational bundle and reusable project memory.

- [ ] Run `node --test scripts/https-ingress-module.test.mjs`.
- [ ] Run `pnpm test:scripts`.
- [ ] Parse the certificate with OpenSSL and verify `DNS:aifar.local` plus key/certificate matching public keys.
- [ ] Parse every shell script with `sh -n`.
- [ ] Run `git diff --check` and inspect `git status --short`.
- [ ] Append a concise problem and conclusion entry to `memory.md` without recording private key contents.
