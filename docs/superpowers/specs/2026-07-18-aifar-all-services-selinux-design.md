# AIFAR All-Services SELinux Script Design

## Goal

Provide one zero-argument shell script that an administrator can run on an openEuler AIFAR host to apply and verify the SELinux configuration required by every supported installed service. The script covers AIFAR Runtime, Docker and `aifar-agent`, MySQL, MySQL Router, Redis, MinIO, Nacos, Keepalived, and the optional HTTPS ingress module.

The script must preserve SELinux Enforcing operation, adapt to the ports and paths actually installed on the host, skip absent services, remain idempotent, and avoid granting permissions to unrelated applications.

## Supported Platform and Invocation

- openEuler 24.03 LTS SP3 on x86_64.
- The managed installation base is `/aifar/apps`.
- DNF uses only repositories already configured on the host, including a mounted offline DVD repository. The script never adds a public repository.
- The administrator runs `bash configure-all-selinux.sh` with no arguments. A non-root invocation may re-execute through non-interactive `sudo` when available.
- SELinux Enforcing and Permissive modes are preserved exactly. If SELinux is Disabled, the script exits non-zero and does not edit `/etc/selinux/config`, boot arguments, or the current mode.

## Repository and Release Layout

The repository adds a focused Linux-only operations module:

```text
extras/selinux/
├── README.md
└── configure-all-selinux.sh
```

`scripts/package-release.mjs` copies `extras/selinux/` into the Linux amd64 release and excludes it from the Windows release. The existing package checksum manifest covers both files.

The aggregate script is self-contained. It does not require the administrator to invoke each application's installer or SELinux helper separately. The Keepalived-specific behavior remains compatible with `extras/keepalived/configure-selinux.sh`, but the aggregate script discovers and applies the installed mappings itself so that copying and running this one script is sufficient.

## Architecture

The script has four internal phases:

1. **Preflight** validates the OS, architecture, privilege, SELinux mode, required commands, and writable transaction directory.
2. **Discovery** checks an explicit AIFAR service whitelist and reads only known systemd units, known configuration files, Docker metadata, and paths below approved installation roots.
3. **Apply** adds persistent port and file-context mappings, then applies file labels to safe targets.
4. **Verify and report** validates every rule that was selected and prints one status per service plus a final summary.

The implementation remains one shell file, but its functions are separated by responsibility: command preparation, transaction recording, service discovery, port policy, file-context policy, verification, rollback, and reporting. No function accepts free-form shell input.

## Service Discovery and Rule Sources

The script does not scan arbitrary processes or every listening socket. Each component is considered installed only when at least one expected ownership marker exists, such as its managed systemd unit or its exact installation root.

| Component | Installation evidence | Port source | File-context source |
| --- | --- | --- | --- |
| Docker and `aifar-agent` | `docker.service`, `containerd.service`, `aifar-agent.service`, or `/aifar/apps/docker` | Parsed Docker `ExecStart`; default `2375` only when the managed unit exists but has no explicit TCP port | Distribution references for Docker data/runtime paths; generic distribution references for the AIFAR agent binary, state, config, and log paths |
| AIFAR Runtime | `/aifar/apps/admin/runtime` and its runtime specification or managed containers | Runtime specification plus published AIFAR container ports; managed defaults are fallback only after installation is proven | Exact bind-mount sources discovered from managed AIFAR containers/specification, labeled for container access |
| MySQL | `aifar-mysql.service` or `/aifar/apps/mysql` | Managed configuration and unit; default `3306` only as installed fallback | Labels derived from distribution MySQL binary, shared-library, configuration, data, log, and runtime reference paths |
| MySQL Router | `aifar-mysql-router.service` or `/aifar/apps/mysql-router` | Router configuration and unit; managed fallback `6446-6449` | Distribution MySQL executable/configuration references where available, otherwise generic executable/config/state references |
| Redis | `aifar-redis.service`, `aifar-redis-sentinel.service`, or `/aifar/apps/redis` | Redis/Sentinel configuration; cluster bus port is the Redis port plus `10000` | Labels derived from distribution Redis binary, configuration, data, log, and runtime reference paths |
| MinIO | `aifar-minio.service` or `/aifar/apps/minio` | Managed environment and unit; installed fallbacks `9000` and `9001` | Generic distribution executable/config/data/log/runtime references because openEuler does not provide an AIFAR-owned MinIO policy |
| Nacos | `aifar-nacos.service` or `/aifar/apps/nacos` | Managed application properties and unit; installed fallbacks `8848`, `9848`, `9849`, and `7848` | Generic distribution executable/config/data/log/runtime references for the JDK and Nacos tree |
| Keepalived | Executable `/aifar/apps/keepalived/sbin/keepalived` or a managed `keepalived.service`; a release-artifact directory alone is not installed evidence | No TCP port mapping; VRRP is IP protocol 112 and remains outside `semanage port` | Labels derived from distribution Keepalived binary, config, helper, state, runtime, and unit references |
| HTTPS ingress | `aifar-https-ingress.service` with a validated module directory | Host ports `80` and `443`, plus configured local upstream ports for verification | Docker-managed private bind-mount labels for `conf.d` and `tls`; module scripts use generic executable labels |

All parsed values must pass strict validation. Ports must be decimal integers from 1 through 65535. Paths must be absolute, canonical, and confined to the exact approved roots for their component. A malformed or out-of-scope installed configuration fails that component instead of falling back silently.

## Port Policy

The script uses only policy types present on the target system. Its managed mappings are:

- Docker remote API: canonical `container_port_t` (`docker_port_t` is a compatibility alias that `semanage port` rejects on openEuler). When Docker is installed but the distribution policy is absent, the script installs the official `container-selinux` package before applying this mapping.
- MySQL and MySQL Router: `mysqld_port_t`.
- Redis, Sentinel, and Redis cluster bus: `redis_port_t`.
- AIFAR HTTP services and MinIO HTTP endpoints: `http_port_t`.
- HTTPS ingress: existing standard `http_port_t` ports are verified; the script does not take ownership of another local mapping.

Nacos has no distribution-specific port type in the current installer contract. The script verifies the four Nacos listeners and their existing SELinux accessibility, but does not mislabel gRPC or Raft as HTTP and does not invent an unconfined custom type. If an AVC `name_bind` denial exists for Nacos, the component fails with the relevant audit evidence and requires a separately reviewed minimal policy update.

For each selected port, the script first checks local and distribution mappings. An absent exact mapping is added when it does not conflict. A matching mapping is unchanged. A conflicting local mapping owned by another type is reported and left untouched; the run fails rather than silently reassigning the port.

## File-Context Policy

Persistent file labels use `semanage fcontext` followed by `restorecon`. The script does not use `chcon` as its durable mechanism.

Recursive relabeling uses `restorecon -x` so a managed directory does not cross into active bind mounts or network namespaces on another filesystem.

For services with distribution policies, the script resolves the type from known standard reference paths using `matchpathcon`. This avoids hard-coding types that may differ across openEuler policy package revisions. A required reference that cannot be resolved causes only that installed component to fail.

For AIFAR-owned applications without distribution-specific policy, the script derives conservative generic types from standard executable, configuration, state, log, and runtime directories. It never labels an entire arbitrary mount or all of `/aifar/apps` with one permissive type. Rules are exact paths or narrowly scoped `path(/.*)?` patterns.

AIFAR Runtime bind mounts are discovered only from containers bearing the managed AIFAR labels and from the managed runtime specification. Only validated sources below `/aifar/apps/admin` are assigned the target system's container-access type.

The HTTPS ingress is a special case because its `docker run` command uses private `:Z` mounts. If the container is running, the script verifies that `conf.d` and `tls` already have a container file type and preserves their MCS categories; it does not run `restorecon` over those live private mounts. If the ingress is stopped, the script may establish the persistent base type and the next `:Z` start remains responsible for assigning private MCS categories.

## Transaction, Rollback, and Idempotence

Before changing a rule, the script writes the previous local mapping to a root-only transaction directory such as `/var/lib/aifar-selinux/transactions/20260718T120000Z/`, using the current UTC time in `YYYYMMDDTHHMMSSZ` format. The transaction contains no credentials or configuration contents.

If any apply or verification step fails, the trap handler reverses only changes made by the current run:

- Newly created local mappings are deleted.
- Updated AIFAR-owned local mappings are restored to their recorded previous value.
- File labels are reapplied from the restored policy where doing so is safe.
- Live HTTPS ingress private MCS labels are never rolled back through `restorecon`.

Successful transactions remain as an audit record. Re-running the script with the same installed configuration makes no policy changes and reports the matching mappings as already applied.

The rollback boundary does not stop, restart, enable, or disable application services. Avoiding service lifecycle changes is required because this maintenance script must not create an application outage merely to apply SELinux metadata.

## Error Handling and Diagnostics

The script uses fail-closed behavior for installed components and safe skipping for absent components:

- Missing service: `SKIPPED` and does not fail the run.
- Installed service with valid rules: `APPLIED` or `UNCHANGED`.
- Installed service with malformed configuration, unavailable required policy type, conflicting local rule, failed relabel, or failed verification: `FAILED` and triggers transaction rollback.

When SELinux tools are missing, the script first tries `dnf -y install policycoreutils policycoreutils-python-utils selinux-policy-targeted` using configured repositories. If the commands remain unavailable, it exits without changing policy.

On failure, the script prints the exact component, path or port, expected type, actual type, transaction path, and recent matching AVC records when `ausearch` is available. It never invokes `audit2allow`, installs a generated `.pp` module, edits system SELinux mode, or converts the host to Permissive.

## Verification and Output

Verification checks the effective mapping and the applied label, not merely the exit status of `semanage`:

- `semanage port -l` confirms every selected port type.
- `matchpathcon` and `stat`/`ls -Z` confirm managed file types.
- Known systemd unit paths are checked to ensure the script did not act on an unrelated unit with the same name.
- HTTPS ingress bind mounts are checked without removing live MCS categories.
- `getenforce` at exit must equal its value at entry.

The final output is designed for direct operator use, for example:

```text
docker             APPLIED
aifar-agent        UNCHANGED
aifar-runtime      APPLIED
mysql              SKIPPED
mysql-router       SKIPPED
redis              APPLIED
minio              SKIPPED
nacos              APPLIED
keepalived          UNCHANGED
https-ingress       APPLIED
SELinux mode        Enforcing (unchanged)
Result              SUCCESS
```

Any `FAILED` component makes the process exit non-zero after rollback.

## Documentation and Packaging

`extras/selinux/README.md` documents:

- Supported OS and installation base.
- The single zero-argument command.
- The fact that only installed AIFAR components are changed.
- Enforcing preservation and Disabled-mode refusal.
- Transaction record location and rollback behavior.
- How to read the per-service summary and AVC diagnostics.
- The explicit exclusion of firewall management and automatic policy generation.

The Linux release package contains this module with executable mode `0755` on the script. Windows packages exclude it.

## Testing and Completion Gate

Script contract tests cover:

- LF line endings, executable archive mode, and Bash syntax.
- No `setenforce`, `/etc/selinux/config` mutation, `audit2allow`, or unrestricted policy module generation.
- The full service whitelist and expected discovery markers.
- Strict path and port validation.
- Installed-versus-absent behavior.
- Add, unchanged, conflict, failure, and current-transaction rollback cases using fake command binaries and temporary policy state.
- Port range handling for MySQL Router and Redis cluster bus ports.
- Reference-label resolution and narrow fcontext patterns.
- HTTPS ingress live `:Z` behavior without destructive `restorecon`.
- Linux-only package inclusion, Windows exclusion, and package checksum coverage.

Completion requires `pnpm test:scripts`, `git diff --check`, a real Linux release tar mode check, and the full local packaging/checksum gate. Target-host execution is a separate mutating validation step and requires explicit authorization.

## Non-Goals

- Disabling SELinux or changing Enforcing/Permissive mode.
- Opening or closing firewall ports.
- Restarting any installed service.
- Scanning or modifying non-AIFAR services.
- Generating broad SELinux policy from AVC logs.
- Installing a custom Nacos process domain without target-host AVC evidence and separate review.
- Removing historical local mappings from prior unrelated administration.
