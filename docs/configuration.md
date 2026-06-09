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
```

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
- **APT-based (apt, mmdebstrap):** dearmored if ASCII-armored and placed in `/etc/apt/trusted.gpg.d/`
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
    install_scap: true          # Install openscap-utils, scap-security-guide, bzip2

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

  - type: s3                   # Upload to S3-compatible storage
    url: https://s3.example.com
    bucket: boot-images
    prefix: compute/
```

S3 publishing reads credentials from the `S3_ACCESS` and `S3_SECRET` environment variables.
