# Keepalived Release Tools Design

## Goal

Ship the verified Keepalived 2.4.2 source archive and its operational scripts inside the AIFAR Linux release package. The module must support offline installation into `/aifar/apps/keepalived`, SELinux Enforcing compatibility, and recoverable uninstallation without adding Linux-only assets to the Windows release.

## Supported Platform

- openEuler 24.03 LTS SP3.
- x86_64 only.
- Keepalived 2.4.2 source build.
- Installation root: `/aifar/apps/keepalived`.
- DNF uses repositories already configured on the target server; no public repository is added by the scripts.

## Repository and Package Layout

The repository owns one focused Linux operations module:

```text
extras/keepalived/
├── README.md
├── keepalived-2.4.2.tar.gz
├── SHA256SUMS
├── install-keepalived-offline.sh
├── configure-selinux.sh
└── uninstall-keepalived.sh
```

`scripts/package-release.mjs` copies this directory to `extras/keepalived/` only for the Linux amd64 release. The Windows package must not contain it. The existing release checksum generator includes all copied files in the package-level `checksums.txt`.

The source archive is fixed to:

- Size: `6350291` bytes.
- SHA256: `76397ad758ae871dfa713b9fc6b4ead754db7964809a3969e40c2d288bc3460b`.

The module-level `SHA256SUMS` records the archive digest so installation can reject a missing, substituted, or damaged source archive before extraction.

## Installation Script

`install-keepalived-offline.sh` is a zero-argument root operation. A non-root caller may re-execute it through `sudo` when available.

The script:

1. Validates openEuler 24.03 LTS SP3 and x86_64.
2. Requires the archive and validates it against the pinned SHA256 before extraction.
3. Installs build dependencies using only the target server's currently configured DNF repositories.
4. Builds and installs Keepalived under `/aifar/apps/keepalived`.
5. Registers the generated systemd unit only when it does not replace an unrelated Keepalived unit.
6. If SELinux is enabled, invokes the colocated `configure-selinux.sh` before installation verification.
7. Verifies the binary version, dynamic libraries, unit target, and SELinux labels when applicable.
8. Does not create an active sample configuration and does not start the service.

Temporary build data is created only below a validated `/tmp/keepalived-offline.*` directory and is removed on success, failure, or interruption.

## SELinux Script

`configure-selinux.sh` preserves the current SELinux mode. It must never call `setenforce`, edit `/etc/selinux/config`, switch to Permissive, or disable SELinux.

When SELinux is Disabled, the script exits with a clear error because it cannot prove Enforcing compatibility. When SELinux is enabled, it:

1. Ensures the distribution SELinux management commands and Keepalived policy definitions are available from configured DNF repositories.
2. Resolves the distribution-owned reference labels for the standard Keepalived binary, configuration, helper-script, state, runtime, and systemd unit locations.
3. Refuses to continue if a required reference label is unavailable rather than inventing a permissive policy.
4. Adds or updates persistent `semanage fcontext` mappings for the corresponding paths under `/aifar/apps/keepalived`.
5. Applies the labels with `restorecon`.
6. Verifies the resulting labels and remains idempotent when run repeatedly.

The script uses the distribution policy as the authority. It does not generate broad `audit2allow` rules and does not grant unrestricted access.

## Uninstall Script

`uninstall-keepalived.sh` is a zero-argument, recoverable root operation. Its order is safety-critical:

1. Resolve and validate that the installation root is exactly `/aifar/apps/keepalived`.
2. Create `/aifar/backups/keepalived-<UTC timestamp>/` with root-only permissions.
3. Copy the installed configuration tree, health/helper scripts, systemd unit, and an uninstall manifest into the backup.
4. Verify the backup is readable and contains the manifest before changing the service.
5. Stop and disable `keepalived.service` so the VIP is removed before files disappear.
6. Remove `/etc/systemd/system/keepalived.service` only when it resolves to the unit inside the custom installation root, then reload systemd.
7. Remove only SELinux file-context mappings owned by this module and reapply the parent directory's default context.
8. Remove the exact validated installation root and verify it is absent.

The uninstaller does not remove DNF packages because they may be shared. It does not delete firewall rules because existing VRRP rules cannot be proven to belong exclusively to this module. It never deletes backups and refuses broad or unresolved deletion targets.

If backup creation or verification fails, uninstallation stops before the service or installation tree is changed. If a later step fails, the backup path is printed prominently for manual recovery.

## Documentation

`extras/keepalived/README.md` documents:

- Supported operating system and architecture.
- The required colocated files.
- Zero-argument installation, standalone SELinux reapplication, and uninstallation commands.
- The fact that installation does not create a production configuration or start Keepalived.
- The backup destination and the firewall/package retention behavior.
- Post-install syntax validation and service commands.

## Packaging and Verification

Script tests cover:

- All six module artifacts exist.
- Shell scripts use LF endings and pass Bash syntax checks.
- The source archive size and SHA256 match the pinned values.
- Installation verifies the archive before extraction and cannot activate a sample configuration.
- SELinux logic contains no mode-degrading operations and uses persistent mappings plus `restorecon`.
- Uninstallation creates and verifies a backup before stopping or deleting, validates the exact installation path, and does not remove shared packages, firewall rules, or backups.
- Linux release staging includes the module while Windows release staging excludes it.
- Package-level checksum verification covers the copied module.

The implementation runs `pnpm test:scripts`, `git diff --check`, and the full release packaging/checksum gate before completion.

## Non-Goals

- Adding Keepalived to the application-store registry or UI.
- Generating the two-node VRRP configuration automatically.
- Starting or enabling Keepalived during installation.
- Removing target-server firewall rules during uninstallation.
- Disabling SELinux or generating permissive local policy from audit logs.
