# AIFAR Runtime Service Modules

The backend discovers installable AIFAR modules from the immediate child
directories in this folder. Every module directory must contain:

- `service.json`: trusted runtime metadata consumed by the backend and UI.
- `Dockerfile`: the offline image build definition.
- The module artifact and any files referenced by the Dockerfile.

`service.json` uses schema `aifar-runtime-service-v1` and defines the stable
module name, localized display name, `java` or `web` kind, application name,
container port, artifact extensions, health path, affinity and
optional `gateway` or `web` role. Role modules are automatically required and
reserved as `gateway` and `web-vue3`; additional business modules should use
`kind: java` without an ingress role.

Directory names and `service.json.name` must match. Names may contain lowercase
letters, digits and hyphens. Ports and roles must be unique across the bundle.
Modules are always listed and processed by directory name; no separate order
field is required.

After adding or changing a module, rescan resources in the panel. The install
dialog and Runtime page read the resulting module list from the backend; no
frontend list needs to be edited.
