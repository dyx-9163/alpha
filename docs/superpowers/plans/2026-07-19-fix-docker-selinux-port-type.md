# Docker SELinux Port Type Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `configure-all-selinux.sh` configure the Docker remote API with the canonical SELinux port type accepted by openEuler, then validate the complete script on `192.168.74.32`.

**Architecture:** Keep the aggregate script and its transactional behavior unchanged. Replace the Docker port type alias with the canonical container policy type, ensure the official `container-selinux` package is present when Docker is installed, strengthen the fake `semanage` boundary so invalid port types are rejected, and verify both local behavior and target-host effective state.

**Tech Stack:** Bash, Node.js test runner, openEuler 24.03 SELinux tools, OpenSSH.

## Global Constraints

- Do not restart services, change firewall rules, or change the SELinux mode.
- Do not write credentials to files, logs, tests, or repository memory.
- Preserve transaction rollback and all existing component mappings.

---

### Task 1: Use the canonical container port type

**Files:**
- Modify: `scripts/selinux-extra.test.mjs`
- Modify: `extras/selinux/configure-all-selinux.sh`
- Modify: `docs/superpowers/specs/2026-07-18-aifar-all-services-selinux-design.md`

**Interfaces:**
- Consumes: `configure_docker()` and `ensure_port_type(component, expected_type, port)`.
- Produces: Docker port mappings using `container_port_t` rather than its compatibility alias `docker_port_t`.

- [ ] **Step 1: Write the failing regression test**

Change the Docker discovered-port expectation to:

```js
expected: ['container_port_t tcp 2376']
```

Make the fake `semanage port -a/-m` reject any type outside the canonical test whitelist.

- [ ] **Step 2: Run the focused test and verify RED**

Run: `node --test --test-name-pattern="Docker applies" scripts/selinux-extra.test.mjs`

Expected: FAIL because `configure_docker()` still requests `docker_port_t`.

- [ ] **Step 3: Implement the minimal fix**

Change only the Docker call to:

```bash
ensure_port_type docker container_port_t "$port"
```

Update the design document's Docker port mapping to the same canonical name.

- [ ] **Step 4: Run local verification**

Run:

```bash
bash -n extras/selinux/configure-all-selinux.sh
node --test scripts/selinux-extra.test.mjs
git diff --check
```

Expected: all commands exit 0 and all SELinux tests pass.

### Task 2: Validate on the authorized openEuler host

**Files:**
- Deploy: `/aifar/apps/selinux/configure-all-selinux.sh` on `192.168.74.32`

**Interfaces:**
- Consumes: the locally verified aggregate script.
- Produces: effective target-host SELinux port and file-context rules plus an operator-visible execution summary.

- [ ] **Step 1: Create the target directory and upload the script**

Use SSH/SCP with strict host-key handling and mode `0755`; do not persist the supplied password.

- [ ] **Step 2: Capture non-mutating preflight evidence**

Check `os-release`, `getenforce`, installed SELinux policy modules, Docker unit `ExecStart`, current TCP port mappings, and current relevant file contexts.

- [ ] **Step 3: Execute the script once**

Run:

```bash
cd /aifar/apps/selinux
bash configure-all-selinux.sh
```

Expected: exit 0 with component statuses and unchanged SELinux mode.

- [ ] **Step 4: Verify effective state and idempotence**

Confirm the Docker API port is mapped to `container_port_t`, relevant existing paths have expected types, no new recent AVC denial was introduced, and a second run exits 0 without further mutations.
