# AIFAR Artifact Bundle Packager Design

## Goal

Provide a Windows CMD entry point that builds a complete AIFAR Runtime artifact-update ZIP from the Alpha Java build outputs and Alpha web `dist` directory. The generated package must be accepted by the existing `update-artifact-bundle` backend validation without manual manifest editing.

## Entry Point and Parameters

The user runs:

```cmd
scripts\package-aifar-artifact-bundle.cmd
```

The CMD wrapper forwards all arguments to a PowerShell implementation. The PowerShell script exposes these independently configurable parameters:

```text
-JavaSourceRoot  default D:\workspace\alpha\backend\alpha-java-cloud
-WebDistRoot     default D:\workspace\alpha\fronted\alpha-web-vue3\dist
-OutputPath      default <current working directory>\aifar-batch-update.zip
```

Example override:

```cmd
scripts\package-aifar-artifact-bundle.cmd ^
  -JavaSourceRoot "E:\build\alpha-java-cloud" ^
  -WebDistRoot "E:\build\alpha-web-vue3\dist" ^
  -OutputPath "E:\release\aifar-full-update.zip"
```

## Java Artifact Selection

The full package contains these nine Java runtime services:

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

`WebDistRoot` must be a directory containing `index.html`. Its contents, not the `dist` directory itself, are compressed into:

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
- one entry for every Java service and `web-vue3`;
- service, module, artifact, fileName, SHA256 and byte size for every entry.

SHA256 and size are calculated from the normalized JAR files and the completed inner web ZIP. Manifest artifact paths always use `/` separators.

## Safety and Error Handling

- Validate all source paths and artifacts before replacing the requested output.
- Build in a unique staging directory adjacent to the output so final replacement stays on the same filesystem.
- Remove staging files in `finally` on success or failure.
- Replace only the explicitly resolved output ZIP after a complete package has been created.
- Never modify Java targets, the web dist directory or deployment resources.
- Return a nonzero exit code with the missing or ambiguous service named in the error.

## Testing

Automated script tests use temporary fixture directories and verify:

1. parameter overrides and default output naming;
2. exact final ZIP paths with no `target` or outer `dist` directory;
3. web inner ZIP roots directly at `index.html`;
4. complete manifest service mapping, SHA256 and byte sizes;
5. failure on a missing or ambiguous runnable artifact;
6. no final output or staging residue after failure.

