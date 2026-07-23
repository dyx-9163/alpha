# AIFAR Bundle Packager Conditional Source Paths Design

## Goal

Allow Java source root and Web dist to be selected independently. The selected services determine which source paths are required; the output ZIP and at least one service are always required.

## Selection Rules

- A Java service is any catalog service other than `web-vue3`.
- Selecting one or more Java services requires a non-empty Java source root.
- Selecting `web-vue3` requires a non-empty Web dist root.
- Selecting both categories requires both source paths.
- Selecting no services disables packaging.
- The output ZIP is always required.
- A path that is not required for the current selection is retained in the form but ignored completely. It may be empty, missing, or otherwise invalid without blocking packaging.

## State Model

`PackagingFormState` will expose computed `RequiresJavaSource` and `RequiresWebDist` properties derived from the ordered selected-service set. `CanPackage` will require:

1. the form is not busy;
2. at least one service is selected;
3. the output ZIP is selected;
4. the Java source root is selected only when `RequiresJavaSource` is true;
5. the Web dist root is selected only when `RequiresWebDist` is true.

Changing service selection does not clear either path.

## Core Validation

`BundlePackager.Validate` will select and classify services before validating source paths:

- Java source normalization, existence checks, and JAR discovery run only when a Java service is selected.
- Web dist normalization, existence checks, `index.html` validation, and output/Web overlap protection run only when `web-vue3` is selected.
- Output normalization and ZIP validation always run.

The packaging context can retain empty strings for unused source paths because the service loop never reads the unused category.

## User Interface

Both source selectors remain enabled while the form is idle so users may choose paths before or after selecting services. The existing path values remain visible when unused.

The requirement message will distinguish these states:

- Web-only: Java source root is not used for this package.
- Java-only: Web dist is not used for this package.
- Mixed: both source paths are required.
- Missing required values: list only the paths required by the current service selection, plus output ZIP or service selection when applicable.

## Tests

Add regression coverage for:

- Web-only state enables packaging with Web dist and output only.
- Java-only state enables packaging with Java source and output only.
- Mixed selection requires both source paths.
- Clearing all services disables packaging.
- Web-only Core packaging accepts an empty or invalid Java path and emits only `web-vue3`.
- Java-only Core packaging accepts an empty or invalid Web path and emits only selected Java services.
- Missing or invalid paths still fail when their category is selected.
- Existing mixed, manifest, hashing, transactional replacement, and all-service tests remain green.

## Compatibility

The bundle format remains `aifar-artifact-bundle-v1`. Service ordering, artifact names, SHA256, byte sizes, ZIP separators, transaction behavior, default all-service selection, and the no-default/no-persistence path policy do not change.
