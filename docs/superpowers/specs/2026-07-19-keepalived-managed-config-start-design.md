# Keepalived Managed Configuration and Startup Design

## Goal

Extend the existing offline Keepalived 2.4.2 operations package so one zero-argument installer can consume a node-local configuration file, generate a production VRRP configuration, install a health probe, manage the peer-scoped VRRP firewall rule, and enable and start Keepalived. The result remains generic across two-node deployments and continues to install under `/aifar/apps/keepalived` on openEuler 24.03 LTS SP3 x86_64.

The design intentionally changes the current package contract. The installer will no longer stop after compiling an inactive binary. It will require an explicit node configuration and will finish only after the generated configuration is valid and `keepalived.service` is active. A failed application health probe does not fail installation: it keeps the VRRP instance in `FAULT` until the local application recovers.

## Scope

This change owns:

- the generic node configuration contract;
- production Keepalived configuration rendering;
- the aggregate HTTP health probe;
- service enablement and startup;
- precise firewalld rule ownership;
- repeat-install backup and rollback;
- the existing Keepalived SELinux helper warning;
- module documentation, checksums, release packaging, and tests.

It does not deploy Keepalived over SSH, configure both nodes from one command, change the application health API, or introduce a general AIFAR control-plane lifecycle for Keepalived.

## Repository and Installed Layout

The repository module becomes:

```text
extras/keepalived/
|-- README.md
|-- keepalived-2.4.2.tar.gz
|-- SHA256SUMS
|-- keepalived.env.example
|-- keepalived.conf.tpl
|-- check-aggregate-health.sh
|-- install-keepalived-offline.sh
|-- configure-selinux.sh
`-- uninstall-keepalived.sh
```

`keepalived.env.example` is copied to `keepalived.env` and edited separately on each node. The installer requires `keepalived.env` beside itself, but the node-specific file is not a fixed repository default and is not generated from machine guesses.

The installed managed files are:

```text
/aifar/apps/keepalived/
|-- sbin/keepalived
|-- etc/keepalived/keepalived.conf
|-- etc/keepalived-health-url
|-- libexec/check-aggregate-health.sh
`-- var/lib/aifar/firewall-rule
```

The repository stores a render template because no single valid Keepalived configuration can represent both nodes. The rendered file under `etc/keepalived/` is the production configuration consumed by the service.

## Node Configuration Contract

Each node supplies these required keys:

```dotenv
KEEPALIVED_LOCAL_IP=192.168.74.132
KEEPALIVED_PEER_IP=192.168.74.133
KEEPALIVED_VIP_CIDR=192.168.74.130/24
KEEPALIVED_INTERFACE=ens160
KEEPALIVED_PRIORITY=150
KEEPALIVED_VIRTUAL_ROUTER_ID=130
KEEPALIVED_HEALTH_URL=http://192.168.74.132:38000/health/aggregate
```

The peer node swaps local and peer addresses, supplies its own health URL and priority, and uses the same VIP/CIDR and virtual router ID.

The installer parses this file as data. It must not source or evaluate it. Parsing uses a fixed key allowlist and rejects unknown keys, duplicate keys, empty values, shell expressions, malformed lines, and unsafe whitespace.

Validation requires:

- distinct valid local and peer IPv4 addresses;
- a valid IPv4 VIP/CIDR whose address is neither node address;
- an existing interface that currently owns the configured local address;
- a VIP route that resolves through the configured interface;
- an integer priority from 1 through 254;
- an integer virtual router ID from 1 through 255;
- an `http://` or `https://` health URL without credentials or unsafe characters;
- a health URL host equal to the local IP, `127.0.0.1`, or `localhost`.

Restricting the URL to the local node prevents a node from remaining eligible by accidentally probing its healthy peer.

## Rendered Keepalived Configuration

Both nodes use `state BACKUP`. Election is determined by the configured priority, and normal Keepalived preemption remains enabled so the higher-priority node reclaims the VIP after its application recovers.

The template renders the following logical configuration:

```conf
global_defs {
    router_id AIFAR_<sanitized-local-ip>
    script_user root
    enable_script_security
}

vrrp_script check_aifar_health {
    script "/aifar/apps/keepalived/libexec/check-aggregate-health.sh"
    interval 2
    timeout 3
    fall 3
    rise 2
    weight 0
}

vrrp_instance AIFAR_VI {
    state BACKUP
    interface <interface>
    virtual_router_id <virtual-router-id>
    priority <priority>
    advert_int 1
    unicast_src_ip <local-ip>
    unicast_peer {
        <peer-ip>
    }
    virtual_ipaddress {
        <vip-cidr> dev <interface>
    }
    track_script {
        check_aifar_health
    }
}
```

Unicast mode avoids multicast dependencies. `weight 0` makes a confirmed probe failure place the instance in `FAULT` instead of merely lowering its election priority. No `nopreempt` directive is rendered.

## Health Probe

`check-aggregate-health.sh` is installed root-owned and non-writable by ordinary users. It reads exactly one URL from the root-owned `etc/keepalived-health-url` file and revalidates the allowed URL shape before using it.

The probe calls `curl` with a one-second connection timeout and a two-second total timeout. It succeeds only when:

1. the request completes within the limit;
2. the HTTP response is in the 2xx range; and
3. the response contains JSON boolean field `"up": true`, allowing ordinary JSON whitespace.

It does not add a `jq` dependency. A timeout, transport error, non-2xx response, missing field, string value such as `"true"`, or boolean `false` returns nonzero.

Keepalived runs the check every two seconds, enters `FAULT` after three consecutive failures, and becomes eligible again after two consecutive successes. The systemd service remains active during application failure, and installation reports the initial failed probe as a warning rather than an installation error.

## Installation Transaction

The installer remains a zero-argument root operation and follows this order:

1. Validate root access, openEuler 24.03 LTS SP3, x86_64, systemd, the pinned source archive, colocated module files, and the node configuration.
2. Complete configuration and interface validation before DNF installation or compilation.
3. Record the existing unit ownership and the service active/enabled state.
4. Back up any existing managed configuration, health probe data, and module state to a UTC-stamped backup directory.
5. Install dependencies from already configured DNF repositories and build Keepalived under `/aifar/apps/keepalived`.
6. Stage the rendered configuration, health URL file, and health script with their final ownership and modes.
7. Validate the staged configuration with the installed binary using `keepalived -t -f`.
8. Apply the existing SELinux configuration helper.
9. Add an owned peer-scoped VRRP firewall rule when firewalld is active.
10. Atomically replace the managed files, reload systemd, enable the service, and start or restart it.
11. Require `systemctl is-active keepalived.service` to succeed, run the health probe once for reporting, and retain the backup.

An existing unrelated Keepalived unit remains protected by the current unit ownership checks. A repeated successful installation intentionally replaces the managed configuration with values from the current `keepalived.env` and restarts the service.

## Firewalld Ownership

When firewalld is active, the installer determines the zone associated with the configured interface, falling back to the firewalld default zone only when the interface has no explicit zone. It constructs a rich rule that accepts VRRP protocol 112 only from `KEEPALIVED_PEER_IP/32`.

The rule is applied to both runtime and permanent configuration. Before adding either form, the installer checks whether the exact rule already exists. Pre-existing rules are never claimed. The module state records the zone, exact rule, and only the runtime/permanent forms actually created by the transaction.

Rollback and uninstall remove only rule forms recorded as module-owned and only when the current rule still exactly matches the record. They do not remove broader, administrator-created, or other service rules. When firewalld is not active, installation logs that firewall management was skipped.

## Rollback

All mutations after backup participate in the current install transaction. On failure, the installer:

- restores the previous configuration, health script, URL file, and ownership state;
- removes only firewalld rule forms created by the failed transaction;
- reloads systemd when unit-visible files changed;
- restores the original enabled/disabled state;
- restarts the previous configuration only when the service was active before installation;
- stops and disables a newly introduced service when no previous active installation existed;
- removes newly staged managed files from a failed first installation.

Rollback errors are reported without hiding the original failure. Backups are retained for recovery and audit. The uninstall script continues its existing backup-first behavior and is extended only to remove precisely owned firewall state.

## SELinux Helper Correction

The current Keepalived helper passes a regular-expression fcontext key containing `\.` through `awk -v`, which can emit `escape sequence '\.' treated as plain '.'` and can make repeat lookup behavior unreliable. The lookup will pass the pattern as literal process data, such as through `ENVIRON`, rather than through awk escape processing. The helper continues to preserve the current SELinux mode and does not weaken policy.

## Security Properties

- Node configuration is parsed, never executed.
- Generated values are validated before interpolation into Keepalived syntax or firewall commands.
- The health URL cannot contain credentials and must address the local node.
- Managed scripts, configuration, and URL data are root-owned and not writable by ordinary users.
- VRRP ingress is limited to the configured peer instead of opening protocol 112 globally.
- Existing service units and pre-existing firewall rules are not claimed or overwritten without ownership proof.
- The installer never disables SELinux and never changes Enforcing to Permissive.

## Tests and Acceptance

Implementation proceeds test-first and updates the existing Keepalived script suite. Tests cover:

- the required repository files and their release modes;
- missing, unknown, duplicate, empty, malformed, and shell-like environment values;
- IPv4, CIDR, interface ownership, route, priority, router ID, and local URL validation;
- deterministic template rendering and `keepalived -t` invocation;
- health responses for 2xx/up=true, false, string true, malformed JSON, non-2xx, timeout, and transport failure;
- fresh installation, repeat installation, initially unhealthy startup, existing firewall rules, and each rollback boundary using fake systemd, firewalld, network, and package commands;
- exact firewalld ownership and uninstall behavior;
- the SELinux awk warning regression;
- Linux-only package inclusion, executable modes, and continued Windows exclusion.

The implementation must pass the focused Keepalived tests, `pnpm test:scripts`, and `pnpm test:local`. Release verification must confirm that the new executable scripts have mode 0755 in the Linux tar archive and that the Windows package still excludes `extras/keepalived`.

The README includes a two-node 192.168.74.132/133 example with VIP 192.168.74.130, configuration preparation, installation, service status, VIP inspection, manual start/stop/restart, health-failure failover, recovery preemption, firewall inspection, and uninstall commands.

## Success Criteria

- The same release package can configure either node solely by changing its colocated `keepalived.env`.
- A valid installation leaves `keepalived.service` enabled and active.
- A locally unhealthy node does not own the VIP, without causing the service installation to fail.
- When both nodes are healthy, the configured higher-priority node owns the VIP.
- When the higher-priority node fails and recovers, the VIP moves away and then returns automatically.
- Re-running the installer is idempotent, backs up the previous configuration, and rolls back on failure.
- Firewall and SELinux changes remain narrow, attributable, and reversible.
