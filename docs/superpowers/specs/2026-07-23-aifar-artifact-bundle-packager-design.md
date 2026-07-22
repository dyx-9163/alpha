# AIFAR Artifact Bundle Packager Design

## Goal

Provide a Windows CMD entry point that builds a complete AIFAR Runtime artifact-update ZIP from the Alpha Java build outputs and Alpha web `dist` directory. The generated package must be accepted by the existing `update-artifact-bundle` backend validation without manual manifest editing.

## Entry Point and CMD Configuration

The user runs:

```cmd
scripts\package-aifar-artifact-bundle.cmd
```

The CMD file contains a clearly marked configuration block near the top:

```cmd
set "JAVA_SOURCE_ROOT=D:\workspace\alpha\backend\alpha-java-cloud"
set "WEB_DIST_ROOT=D:\workspace\alpha\fronted\alpha-web-vue3\dist"
set "OUTPUT_PATH=%CD%\aifar-batch-update.zip"
```

Users change source or output locations by editing these CMD settings. Paths are not exposed as normal invocation arguments. The CMD wrapper passes its configured paths internally to the PowerShell implementation.

## Service Selection

With no argument, the script builds all ten runtime services:

```cmd
scripts\package-aifar-artifact-bundle.cmd
```

This is equivalent to:

```cmd
scripts\package-aifar-artifact-bundle.cmd all
```

To build a partial package, the first CMD argument is a comma-separated list:

```cmd
scripts\package-aifar-artifact-bundle.cmd gateway,im,meeting,web-vue3
```

Service names are normalized to lowercase, surrounding whitespace is ignored and duplicates are removed. Supported values are `oauth`, `permission`, `system`, `file`, `message`, `im`, `contacts`, `meeting`, `gateway` and `web-vue3`. Any unknown or empty list item fails before the output is replaced. `all` cannot be combined with individual names.

Only selected artifacts are validated, copied and listed in `manifest.json`. Entries use the canonical runtime order regardless of input order.

## Java Artifact Selection

The complete package contains these nine Java runtime services:

| Service | Module | Expected build directory |
| --- | --- | --- |
| oauth | alpha-oauth | `alpha-oauth\alpha-oauth-server\target` |
| permission | alpha-permission | `alpha-permission\alpha-permission-server\target` |
| system | alpha-system | `alpha-system\alpha-system-server\target` |
| file | alpha-file | `alpha-file\alpha-file-server\target` |
| message | alpha-message | `alpha-message\alpha-message-server\target` |
| im | alpha-im | `alpha-im\alpha-im-core\target` |
| contacts | alpha-contacts | `alpha-contacts\alpha-contacts-core\target` |
| meeting | alpha-meeting | `alpha-meeting\alpha-meeting-core\target` |
| gateway | alpha-gateway | `alpha-gateway\target` |

Each target directory must contain exactly one runnable `${module}-*.jar` after excluding source, Javadoc, test, plain and `original-*` artifacts. Missing or ambiguous runnable JARs fail the build instead of selecting a possibly incorrect library JAR.

The generated update package removes the Maven `target` directory and normalizes names:

```text
artifacts/<service>/<module>.jar
```

## Web Artifact

When `web-vue3` is selected, `WEB_DIST_ROOT` must be a directory containing `index.html`. Its contents, not the `dist` directory itself, are compressed into:

```text
artifacts/web-vue3/web-vue3.zip
```

Opening the inner ZIP therefore shows `index.html`, `assets/` and other web files at its root.

## Manifest and Final Package

The final ZIP has this structure:

```text
aifar-batch-update.zip
├── manifest.json
└── artifacts/
    ├── oauth/alpha-oauth.jar
    ├── permission/alpha-permission.jar
    ├── system/alpha-system.jar
    ├── file/alpha-file.jar
    ├── message/alpha-message.jar
    ├── im/alpha-im.jar
    ├── contacts/alpha-contacts.jar
    ├── meeting/alpha-meeting.jar
    ├── gateway/alpha-gateway.jar
    └── web-vue3/web-vue3.zip
```

`manifest.json` contains:

- schema `aifar-artifact-bundle-v1`;
- app `aifar`;
- kind `aifar-service-artifacts`;
- one entry for every selected Java service and, when selected, `web-vue3`;
- service, module, artifact, fileName, SHA256 and byte size for every entry.

SHA256 and size are calculated from the normalized JAR files and the completed inner web ZIP. Manifest artifact paths always use `/` separators.

## Safety and Error Handling

- Validate the requested service list and only its required source paths and artifacts before replacing the configured output.
- Build in a unique staging directory adjacent to the output so final replacement stays on the same filesystem.
- Remove staging files in `finally` on success or failure.
- Replace only the explicitly resolved output ZIP after a complete package has been created.
- Never modify Java targets, the web dist directory or deployment resources.
- Return a nonzero exit code with the missing or ambiguous service named in the error.

## Testing

Automated script tests use temporary fixture directories and verify:

1. CMD configuration defaults and output naming;
2. no argument and explicit `all` both select all ten services;
3. comma-separated selection includes only requested services in canonical order;
4. rejection of unknown, empty or `all`-plus-specific selections, with duplicates collapsed;
5. exact final ZIP paths with no `target` or outer `dist` directory;
6. web inner ZIP roots directly at `index.html`;
7. complete manifest mapping, SHA256 and byte sizes for the selected services;
8. failure on a missing or ambiguous selected artifact;
9. no final output or staging residue after failure.
