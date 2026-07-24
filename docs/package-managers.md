# Package Manager Support

| Backend | Scratch builds | Parent builds | Groups / patterns | Modules | Configurable options |
| --- | --- | --- | --- | --- | --- |
| **DNF** (RHEL, Rocky, AlmaLinux, Fedora) | ✅ (installroot) | ✅ | ✅ groups | ✅ enable/install/disable | 6 options |
| **Zypper** (openSUSE, SLES) | ✅ | ✅ | ⚠️ patterns only | ❌ | 6 options |
| **APT** (Debian, Ubuntu) | ❌ — use mmdebstrap | ✅ | ❌ | ❌ | 3 options |
| **mmdebstrap** (Debian, Ubuntu) | ✅ (Debian bootstrap) | ❌ | ❌ | ❌ | suite + mirror required |

See [configuration.md](configuration.md#backend-options) for the full option list per backend.

## When to use which

- **DNF / Zypper** — RPM-based distros. Both support scratch and parent builds.
- **APT** — Debian-family parent builds (layering on top of `debian:bookworm`, `ubuntu:24.04`, etc.).
- **mmdebstrap** — Debian-family scratch builds. The APT backend does not support scratch builds; use mmdebstrap instead.

## DNF multi-version note

DNF supports any RHEL-family release through the `releasever` option (e.g. `"9"`, `"10"`, `"40"`). One unified container image can build Rocky 9, Rocky 10, AlmaLinux, Fedora, etc. See [container-usage.md](container-usage.md#multi-version-dnf-builds) for an example.

## Build-step ordering notes

- **Most backends** (dnf, zypper, apt): repo files, GPG keys, `layer.files`, and `layer.directories` are written into the image *before* the install step, so the package manager sees them.
- **mmdebstrap**: the bootstrap runs *first*, before any repo/file/key writes — mmdebstrap refuses to bootstrap into a non-empty target directory. Files, repos, and GPG keys configured in the layer are written after the bootstrap completes; they end up in the image and are available to `layer.actions.commands` and to apt at runtime, but they cannot influence the bootstrap itself. Use the `mirror`/`suite` options to control where mmdebstrap pulls from.
- **DNF module operations** (`enable`/`disable`/`install`/`reset`) always run before package and group installs, in both scratch and parent modes, so an enabled stream takes effect for the installs that follow.
