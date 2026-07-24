# Configuration Reference

Configuration is a single YAML file with three top-level sections — `meta`, `layer`, and `publish` — plus an optional `layer.openscap` block for security scanning.

A minimal example lives in the [main README](../README.md#quick-start). Complete examples per backend live under [`tests/`](../tests/); see [examples.md](examples.md) for an annotated index.

## `meta`

Image metadata and base image.

```yaml
meta:
  name: my-image           # Image name (required)
  tags:                    # Image tags (required, one or more)
    - "1.0"
    - "latest"
  from: scratch            # Base: 'scratch' or 'registry.io/image:tag'
  from-tls-verify: true    # Optional: verify TLS when pulling base (default: true)
  labels:                  # Optional: custom OCI image labels
    org.example.team: hpc
```

### Image labels

Every build automatically stamps `org.openchami.image.*` labels into the image (name, package manager, parent image, tags, build date, repo/package/group lists). Custom `meta.labels` entries are merged on top and **override** an auto-generated label with the same key.

Labels are applied to the container once, before publishing, so every publish destination that produces an OCI image (local storage, registry) carries them — including registry-only publishes. SquashFS and S3 outputs are filesystem images and have no label metadata.

## `layer`

Defines how the image layer is built.

### `layer.manager`

```yaml
layer:
  manager:
    name: dnf              # dnf | zypper | apt | mmdebstrap
    config: |              # Optional: package manager config file content
      [main]
      gpgcheck=1
    options:               # Optional: backend-specific options (see below)
      install-weak-deps: "false"
      best: "true"
```

#### Backend options

All option values are strings (`"true"` / `"false"` / etc.).

**APT (Debian/Ubuntu, parent builds only)**
| Option | Default | Description |
| --- | --- | --- |
| `install-recommends` | `"false"` | Install recommended packages |
| `install-suggests` | `"false"` | Install suggested packages |
| `allow-unauthenticated` | `"false"` | Allow unsigned packages |

**DNF (RHEL/Rocky/AlmaLinux/Fedora)**
| Option | Default | Description |
| --- | --- | --- |
| `install-weak-deps` | `"true"` | Install weak dependencies |
| `best` | `"true"` | Use best package versions |
| `skip-broken` | `"false"` | Skip broken packages |
| `allowerasing` | `"false"` | Allow erasing packages for dependencies |
| `nobest` | `"false"` | Don't limit to best candidates |
| `releasever` | — | Override RHEL/distro release version (e.g. `"9"`, `"10"`, `"40"`). **Required for scratch builds.** |

**Zypper (openSUSE/SLES)**
| Option | Default | Description |
| --- | --- | --- |
| `repopath` | `"/etc/zypp/repos.d"` | Repository directory path |
| `no-recommends` | `"false"` | Don't install recommended packages |
| `no-gpg-checks` | `"false"` | Skip GPG verification |
| `force-resolution` | `"false"` | Auto-resolve conflicts |

**mmdebstrap (Debian/Ubuntu, scratch builds only)**
| Option | Default | Description |
| --- | --- | --- |
| `suite` | — | Debian/Ubuntu release (e.g. `"bookworm"`). **Required.** |
| `mirror` | — | Package mirror URL (e.g. `"http://deb.debian.org/debian"`). **Required.** |
| `variant` | `"minbase"` | Bootstrap variant |
| `mode` | `"fakechroot"` | Execution mode |

### `layer.repos`

```yaml
  repos:
    - path: /etc/yum.repos.d/my-repo.repo
      content: |                                  # Inline content (preferred)
        [my-repo]
        name=My Repository
        baseurl=https://...
        gpgcheck=1
        gpgkey=file:///etc/pki/rpm-gpg/RPM-GPG-KEY-myrepo
      gpg: https://example.com/RPM-GPG-KEY-myrepo  # Automatic GPG key import
    - path: /etc/yum.repos.d/other.repo
      src: ./local-file.repo                       # Copy from local file
    - path: /etc/yum.repos.d/remote.repo
      url: https://example.com/repo.repo           # Download from URL
```

Use exactly one of `content`, `src`, or `url` per entry.

**GPG key import.** The optional `gpg` field fetches a key over HTTP (60-second timeout, cancellable) and installs it in the backend's trust store:

- **RPM-based (dnf, zypper):** imported via `rpm --import`
- **APT-based (apt, mmdebstrap):** dearmored if ASCII-armored and placed in `/etc/apt/trusted.gpg.d/<name>.gpg`, where `<name>` is derived from the repo file's basename (e.g. `/etc/apt/sources.list.d/toolchain.sources` → `/etc/apt/trusted.gpg.d/toolchain.gpg`). Each repo gets its own keyring file, so multiple apt repos with separate `gpg:` keys no longer overwrite one another. Because the key lands in `trusted.gpg.d` it is trusted globally, so a plain deb822 stanza needs no `Signed-By:` line — supplying `gpg:` is enough.
- **Scratch builds:** the trust store under the new root filesystem is targeted on the host
- **Parent builds:** the key is placed inside the container

The user-supplied URL is **never** interpolated into a shell command — the fetch happens in Go and the backend only sees a local file path. Per-repo key import failures are logged as warnings; the build continues so a repo that works without GPG (or whose key the user has installed by other means) is not blocked.

If `gpg` is omitted you must either set `gpgcheck=0` in the repo config or install the key out-of-band.

### `layer.files`

```yaml
  files:
    - path: /etc/custom-config
      content: |                  # Inline content
        key=value
    - path: /usr/local/bin/script.sh
      src: ./scripts/script.sh    # Copy from local file
      mode: "0755"                # Optional: octal permissions
    - path: /etc/downloaded-config
      url: https://example.com/config
      mode: "0644"
```

- `path` (required): destination path in the image
- `content` | `src` | `url` (required, pick one): file source
- `mode` (optional): file permissions in octal (e.g. `"0755"`, `"0644"`)

### `layer.directories`

Recursively copy a host directory tree into the image in a single buildah operation. Use this instead of declaring every file under `layer.files` when you have a stage directory, a roles tree, or any payload with more than a handful of files.

```yaml
  directories:
    - path: /opt/myapp                # destination dir in the image (required)
      src: ./build/myapp              # host directory (required)
      mode: "0755"                    # optional, uniform mode (see notes)
      owner: "1000:1000"              # optional, "uid:gid" or "user:group"
      preserve_ownership: false       # optional, mutually exclusive with owner
      contents_only: true             # optional, default true (see notes)
      excludes:                       # optional, .containerignore-style patterns
        - "*.tmp"
        - cache
```

Fields:

- `path` (required): destination directory in the image
- `src` (required): host directory; relative paths resolve against the current working directory (same as `ansible.playbook`), not the config file's location
- `mode` (optional): octal permission string. **Applied uniformly to files and directories** — buildah doesn't expose separate file/dir modes through its public `Add` API. Omit `mode` to preserve host permissions as-is; if you need different modes for files vs directories, set them on the host before referencing the tree
- `owner` (optional): `"uid:gid"` or `"user:group"`. Applied to every copied entry
- `preserve_ownership` (optional, default `false`): when `true`, keep the host's UID/GID instead of buildah's default of resetting to `0:0`. Has no effect when `owner` is set, and validation rejects setting both
- `contents_only` (optional, default `true`): true copies `src/.` into `path` (matches `cp -a src/. dest/`); false copies `src` itself as a subdirectory under `path` (matches Dockerfile `COPY src dest/` semantics)
- `excludes` (optional): list of `.containerignore`-style patterns. Evaluated by the same matcher buildah uses internally, so the cache hash and the actual copy see the same file set. Common patterns: `"*.tmp"`, `cache`, `build/`

**Caching:** the layer tag hash includes the contents and structure of `src` (filtered by `excludes`), plus host modes when `mode` is unset and host ownership when `preserve_ownership` is true and `owner` is empty. Editing a file under `src` invalidates the cache and triggers a rebuild; editing an excluded file does not. Mtimes are deliberately ignored so a fresh `git clone` doesn't invalidate every cache.

**URL/tarball sources are not supported** — use `layer.files` with a `url:` if you need to drop a downloaded archive, then extract it via a `command:` step.

### `layer.actions`

```yaml
  actions:
    install:
      packages:                   # Individual packages
        - vim
        - wget
      groups:                     # Package groups (DNF only)
        - Development Tools
      modules:                    # DNF modules (DNF only)
        - name: nodejs
          stream: "18"
          action: install         # enable | install | disable
      remove_packages:            # Packages to remove
        - kernel-debug
        - man-pages
        - linux-firmware
    commands:
      - run: systemctl enable myservice    # Single command
      - script: |                          # Multi-line script
          #!/bin/bash
          echo "Setting up..."
          dnf clean all
```

#### Package removal

`remove_packages` runs after the install step and is useful for trimming debug packages, docs, unused firmware, and build tools.

- RPM-based systems (dnf, zypper) use `rpm -e --nodeps`
- Debian-based systems (apt, mmdebstrap) use `dpkg --remove --force-depends`
- For **scratch builds** the command runs on the host targeting the mounted root (e.g. `rpm --root <mount> -e --nodeps …`), because a freshly-bootstrapped scratch root may not yet be able to exec the package manager
- For **parent builds** it runs inside the container
- **Failures fail the build.** A common mistake is listing a package that isn't installed — the package manager returns non-zero and the build stops. List only packages you know are present after the install step.

Minimal-image example:

```yaml
layer:
  manager:
    name: dnf
    options:
      install-weak-deps: "false"
  actions:
    install:
      groups:
        - Minimal Install
      packages:
        - kernel
        - systemd
      remove_packages:
        - kernel-debug
        - kernel-debug-core
        - man-db
        - man-pages
        - linux-firmware
        - dracut-config-rescue
```

### `layer.openscap` (optional)

Security compliance scanning and vulnerability assessment.

```yaml
layer:
  openscap:
    install_scap: true          # Install the oscap scanner + SCAP Security Guide content + bzip2.
                                # RPM distros: openscap-utils, scap-security-guide, bzip2.
                                # Debian/Ubuntu: openscap-utils, bzip2, ssg-base, ssg-debian, ssg-debderived
                                # (Debian splits the SSG content into ssg-* packages).

    scap_benchmark: true        # Run XCCDF security benchmark scan
    profile: "xccdf_org.ssgproject.content_profile_stig"
    benchmark_path: "/usr/share/xml/scap/ssg/content/ssg-rl9-ds.xml"

    oval_eval: true             # Run OVAL vulnerability evaluation
    oval_url: "https://www.redhat.com/security/data/oval/v2/RHEL9/rhel-9.oval.xml.bz2"

    # Optional: custom result paths (defaults shown)
    results_path: "/root/scan.xml"
    remediate_path: "/root/remediate.sh"
    oval_result_path: "/root/vulnerabilities.xml"
```

Features:
- **XCCDF benchmarks** — test against profiles like DISA STIG, CIS, PCI-DSS
- **OVAL evaluations** — check for known CVEs in installed packages
- **Remediation scripts** — generate scripts to fix findings
- **Compliance reports** — detailed XML saved in the container

Common SCAP profiles:
- `xccdf_org.ssgproject.content_profile_stig` — DISA STIG (DoD requirements)
- `xccdf_org.ssgproject.content_profile_cis` — CIS Benchmarks
- `xccdf_org.ssgproject.content_profile_pci-dss` — PCI-DSS compliance
- `xccdf_org.ssgproject.content_profile_ospp` — OSPP / Common Criteria

List available profiles inside the build: `oscap info /usr/share/xml/scap/ssg/content/ssg-rl9-ds.xml`.

## `publish`

One or more publish targets. Each runs after a successful build.

```yaml
publish:
  - type: local                # Commit to local container storage

  - type: squashfs             # Create SquashFS image
    path: /output/images       # Output directory; file is written as
                               # <meta.name>-<meta.tags[0]>.squashfs

  - type: registry             # Push to container registry
    url: registry.example.com/myorg
    tls-verify: false          # Optional: disable TLS verification

  - type: s3                   # Upload boot artifacts to S3-compatible storage
    url: https://s3.example.com
    bucket: boot-images
    prefix: compute/
```

S3 publishing reads credentials from the `S3_ACCESS` and `S3_SECRET` environment variables.

The S3 publisher extracts the rootfs (SquashFS), kernel, and initramfs and uploads them as a self-contained directory per tag:

```
<prefix><tag>/<arch>/rootfs.squashfs
<prefix><tag>/<arch>/vmlinuz
<prefix><tag>/<arch>/initramfs.img
```

`<tag>` is `meta.tags[0]` (the content tag in a manifest build). The `<arch>` segment is present for multi-arch manifest builds and omitted otherwise. The same layout is produced whether an image reaches S3 via a build-time `s3` publish block or via [`promote --to s3`](promote.md).

## Manifests

A **manifest** is a YAML file that describes a DAG of layers. Each layer references a config file (of the shape above) and declares which other layers it depends on. `image-thrillhouse` computes a deterministic hash tag for each layer, so a child layer's `from:` can pin the exact parent it was built against via `{{ .parent_tag }}` — no manual tag bookkeeping.

The tag is a truncated (128-bit) sha256 over the layer's **rendered** config — the template after applying var files, CLI `--var` overrides, and computed vars — plus the contents of any `src:` files/repos and `directories` trees the rendered config references, referenced URLs, and the tags of the layer's direct parents (which chain the full ancestry, so any change in an ancestor produces a new tag for every descendant).

Because the hash covers the render's *output*, a variable participates in a layer's tag exactly when the layer's rendered config consumes it:

- Editing a var file key (or comment) that no template references does **not** change any tag — no spurious rebuilds when a shared var file changes for someone else's layer.
- Two builds differing in a `--var` the template consumes get distinct tags (important with `--skip-if-exists`); a `--var` nothing references leaves tags unchanged, which is correct — the images are identical.
- Templated `src:` paths (e.g. `src: "{{ .payload_dir }}/foo"`) resolve before hashing, so edits to the referenced file's contents change the tag like any literal `src:`.
- `src:` entries inside an active `{{ if }}`/`{{ range }}` block are resolved and content-hashed like any other. A branch that renders to nothing contributes nothing to the hash — flipping the condition changes the rendered output, and therefore the tag.

Since `{{ .tag }}` is the layer's own hash, it can't feed its own computation: the hash-input render binds it to a fixed sentinel, and the real value is substituted for the actual build render. It's the only variable the two renders differ on.

Manifests replace the pattern of hand-writing one config per (distro × arch) with a single template plus per-arch var files.

Once a layer's content-tagged image has been built and tested, give it a human-readable release tag with [`promote`](promote.md) — it retags the tested image in place, no rebuild.

### Minimal example

```yaml
# tests/manifests/rocky.yaml
layers:
  - name: rocky-base
    config: ../rocky/templates/rocky-base.yaml
    var_files:
      - ../rocky/templates/x86_64.yaml
  - name: rocky-compute
    config: ../rocky/templates/rocky-compute.yaml
    var_files:
      - ../rocky/templates/x86_64.yaml
    depends_on:
      - rocky-base
```

Fields:

- `name` (required): unique identifier for the layer within the manifest.
- `config` (required): path to the layer's config file. Relative paths resolve against the manifest's directory.
- `var_files` (optional): var files applied when rendering `config`.
- `depends_on` (optional): logical layer names this layer builds on top of.

Build a specific layer:

```bash
image-thrillhouse build --manifest tests/manifests/rocky.yaml --layer rocky-compute
```

### Multi-arch manifests

Declare an `architectures:` block at the top of the manifest and each `layers[]` entry expands into one concrete build per arch it targets. The same template is reused across arches; per-arch differences (repo URLs, arch-only packages, etc.) live in the arch var files.

```yaml
# tests/manifests/rocky-multiarch.yaml
architectures:
  - name: x86_64
    var_files: [../rocky/templates/x86_64.yaml]
  - name: aarch64
    var_files: [../rocky/templates/aarch64.yaml]

layers:
  - name: rocky-base
    config: ../rocky/templates/rocky-base.yaml
  - name: rocky-compute
    config: ../rocky/templates/rocky-compute.yaml
    depends_on:
      - rocky-base
```

This expands to four concrete build targets — `rocky-base-x86_64`, `rocky-base-aarch64`, `rocky-compute-x86_64`, `rocky-compute-aarch64` — with `depends_on` rewired so each child pins its same-arch parent.

Build a specific expansion by naming the **logical** layer and passing `--arch`:

```bash
image-thrillhouse build --manifest tests/manifests/rocky-multiarch.yaml \
  --layer rocky-compute --arch aarch64
```

`--arch` defaults to the host arch when omitted (`amd64` → `x86_64`, `arm64` → `aarch64`, `386` → `i386`, other values pass through).

### Restricting a layer to a subset of arches

Some packages only exist for one arch, or a whole layer might not make sense on every target. Add `arches:` to opt a layer into a subset of the manifest's declared architectures:

```yaml
layers:
  - name: base
    config: base.yaml
  - name: hpc-tuning
    config: hpc-tuning.yaml
    arches: [x86_64]       # skip aarch64 entirely
    depends_on: [base]
```

Rules:

- `arches:` values must be a subset of the manifest's `architectures[]` names.
- A layer that builds for arch `A` cannot depend on a layer that doesn't. If `hpc-tuning` above tried to depend on an `aarch64`-only parent, `image-thrillhouse` would refuse to load the manifest and tell you which arches to reconcile.
- `arches:` outside a manifest that has an `architectures:` block is an error — the field only makes sense with expansion.

For **per-package** arch differences (as opposed to whole layers), keep one shared template and let the arch var files supply an arch-specific package list — the template references e.g. `{{ .extra_packages }}` and each arch var file defines its own list.

### Template variables injected by manifests

When rendering a manifest layer's config, `image-thrillhouse` injects the following into the template's variable scope. These take precedence over CLI vars and var files.

| Variable | Value |
| --- | --- |
| `tag` | This layer's computed hash. Use in `meta.tags`. |
| `arch` | This layer's arch name (multi-arch manifests only). |
| `<parent>_tag` | Each direct parent's hash, keyed by the parent's **logical** name (hyphens → underscores). |
| `parent_tag` | Alias for the one parent's hash when the layer has exactly one direct parent. |

A multi-arch template can stay arch-agnostic by using `parent_tag` (or `{{ .rocky_base_tag }}`) — both resolve to the same-arch parent for whichever arch is being built.

```yaml
# rocky-compute.yaml — used unchanged across arches
meta:
  name: rocky-compute
  tags: ["{{ .arch }}-{{ .tag }}"]
  from: localhost/rocky-base:{{ .parent_tag }}
```

### CLI

```
image-thrillhouse build|validate|render \
  --manifest <path> \
  --layer <logical-name> \
  [--arch <arch>]           # required for multi-arch, defaults to host
  [--var-file <path>] [--var key=value]
  [--skip-if-exists]        # build only; skip when publishers report the image exists
```

- `--layer` names a **logical** layer. In a multi-arch manifest, passing a concrete arch-suffixed name is rejected — the error message will point at the correct `--layer + --arch` pair.
- `--config` and `--manifest` are mutually exclusive.
- `--arch` requires `--manifest`.

### Var precedence

When rendering a manifest layer, values from multiple sources are merged — highest wins:

1. Manifest-computed vars (`tag`, `arch`, `parent_tag`, `<parent>_tag`).
2. CLI `--var key=value`.
3. CLI `--var-file`.
4. Layer `var_files`.
5. Architecture `var_files`.

This lets a shared template default arch values in `architectures[].var_files`, override for a specific layer in `layers[].var_files`, and pin ad-hoc values at the CLI without editing the manifest.

**Undefined variables are a hard error.** Rendering fails loudly if a template references a key that none of the sources above provides — a missing variable silently rendering to nothing (e.g. an empty repo `baseurl`) would ship a broken image. Every `{{ .foo }}` a template uses must be defined somewhere. For a genuinely optional list, declare it as an empty list (`foo: []`) and guard it with `{{ range .foo }}…{{ else }}…{{ end }}`; to branch on optional presence without a hard field access, use `{{ if index . "foo" }}`.

### Example manifests

- [`tests/manifests/rocky.yaml`](../tests/manifests/rocky.yaml) — minimal single-arch DAG (dnf).
- [`tests/manifests/bookworm.yaml`](../tests/manifests/bookworm.yaml) — Debian single-arch (mmdebstrap + apt).
- [`tests/manifests/rocky-multiarch.yaml`](../tests/manifests/rocky-multiarch.yaml) — Rocky with `architectures:` expansion.
- [`tests/manifests/suse-multiarch.yaml`](../tests/manifests/suse-multiarch.yaml) — openSUSE Leap multi-arch (zypper).
- [`tests/manifests/cross-backend.yaml`](../tests/manifests/cross-backend.yaml) — three roots (apt/dnf/zypper) in one manifest.
