# Example Configurations

Complete, runnable configs live under [`tests/`](../tests/). They double as the corpus for the integration test suite — every file listed here is exercised by `run-all-tests.sh`.

## DNF (Rocky Linux)

- [`tests/dnf/rocky-scratch.yaml`](../tests/dnf/rocky-scratch.yaml) — Minimal scratch build
- [`tests/dnf/rocky-scratch-options.yaml`](../tests/dnf/rocky-scratch-options.yaml) — Scratch with DNF backend options
- [`tests/dnf/rocky-scratch-groups.yaml`](../tests/dnf/rocky-scratch-groups.yaml) — Scratch with package groups
- [`tests/dnf/rocky-scratch-full.yaml`](../tests/dnf/rocky-scratch-full.yaml) — Scratch with repos, files, commands
- [`tests/dnf/rocky-parent.yaml`](../tests/dnf/rocky-parent.yaml) — Layered on a parent image
- [`tests/dnf/rocky-parent-groups.yaml`](../tests/dnf/rocky-parent-groups.yaml) — Parent build using groups
- [`tests/dnf/rocky-parent-modules.yaml`](../tests/dnf/rocky-parent-modules.yaml) — Parent build using DNF modules
- [`tests/dnf/rocky-parent-commands.yaml`](../tests/dnf/rocky-parent-commands.yaml) — Parent build with run/script commands
- [`tests/dnf/rocky-parent-directories.yaml`](../tests/dnf/rocky-parent-directories.yaml) — Parent build copying a host directory tree (`layer.directories`)
- [`tests/dnf/rocky-parent-files-mode.yaml`](../tests/dnf/rocky-parent-files-mode.yaml) — Parent build with per-file permission modes
- [`tests/dnf/rocky-parent-squashfs.yaml`](../tests/dnf/rocky-parent-squashfs.yaml) — Parent build with a SquashFS publisher alongside local

## APT / mmdebstrap (Debian / Ubuntu)

- [`tests/apt/debian-scratch.yaml`](../tests/apt/debian-scratch.yaml) — mmdebstrap scratch build (APT scratch is not supported)
- [`tests/apt/debian-scratch-options.yaml`](../tests/apt/debian-scratch-options.yaml) — Scratch with mmdebstrap options
- [`tests/apt/debian-scratch-mirror.yaml`](../tests/apt/debian-scratch-mirror.yaml) — Scratch using a non-default mirror
- [`tests/apt/debian-scratch-full.yaml`](../tests/apt/debian-scratch-full.yaml) — Scratch with repos, files, commands
- [`tests/apt/debian-parent.yaml`](../tests/apt/debian-parent.yaml) — APT parent build
- [`tests/apt/debian-parent-files.yaml`](../tests/apt/debian-parent-files.yaml) — Parent build that adds files
- [`tests/apt/debian-parent-commands.yaml`](../tests/apt/debian-parent-commands.yaml) — Parent build with run/script commands
- [`tests/apt/debian-parent-tasks.yaml`](../tests/apt/debian-parent-tasks.yaml) — Parent build combining install + tasks

## Zypper (openSUSE / SLES)

- [`tests/zypper/suse-scratch.yaml`](../tests/zypper/suse-scratch.yaml) — Minimal scratch build
- [`tests/zypper/suse-scratch-options.yaml`](../tests/zypper/suse-scratch-options.yaml) — Scratch with Zypper backend options
- [`tests/zypper/suse-scratch-with-licenses.yaml`](../tests/zypper/suse-scratch-with-licenses.yaml) — Scratch with `auto-agree-with-licenses`
- [`tests/zypper/suse-scratch-patterns.yaml`](../tests/zypper/suse-scratch-patterns.yaml) — Scratch using SUSE patterns (groups)
- [`tests/zypper/suse-scratch-full.yaml`](../tests/zypper/suse-scratch-full.yaml) — Scratch with repos, files, commands
- [`tests/zypper/suse-parent.yaml`](../tests/zypper/suse-parent.yaml) — Parent build
- [`tests/zypper/suse-parent-files.yaml`](../tests/zypper/suse-parent-files.yaml) — Parent build that adds files
- [`tests/zypper/suse-parent-patterns.yaml`](../tests/zypper/suse-parent-patterns.yaml) — Parent build using patterns
- [`tests/zypper/suse-parent-commands.yaml`](../tests/zypper/suse-parent-commands.yaml) — Parent build with run/script commands

## Validation negatives

Intentionally invalid configs used to verify the `validate` subcommand rejects them:

- [`tests/rocky/invalid-dnf-test.yaml`](../tests/rocky/invalid-dnf-test.yaml) — unknown DNF option
- [`tests/rocky/conflicting-dnf-test.yaml`](../tests/rocky/conflicting-dnf-test.yaml) — `best: true` and `nobest: true` together
- [`tests/opensuse/invalid-zypper-test.yaml`](../tests/opensuse/invalid-zypper-test.yaml) — unknown Zypper option
- [`tests/apt/invalid-option.yaml`](../tests/apt/invalid-option.yaml) — unknown apt option
- [`tests/apt/invalid-no-suite.yaml`](../tests/apt/invalid-no-suite.yaml) — mmdebstrap missing required `suite`

## Manifests

Multi-layer DAG builds and multi-arch expansion live under [`tests/manifests/`](../tests/manifests/). See [Manifests](configuration.md#manifests) for the schema.

- [`tests/manifests/rocky.yaml`](../tests/manifests/rocky.yaml) — minimal single-arch DAG (base → compute)
- [`tests/manifests/bookworm.yaml`](../tests/manifests/bookworm.yaml) — Debian single-arch (mmdebstrap + apt)
- [`tests/manifests/rocky-multiarch.yaml`](../tests/manifests/rocky-multiarch.yaml) — Rocky with an `architectures:` block; one template per arch
- [`tests/manifests/suse-multiarch.yaml`](../tests/manifests/suse-multiarch.yaml) — openSUSE Leap multi-arch (zypper)
- [`tests/manifests/cross-backend.yaml`](../tests/manifests/cross-backend.yaml) — three roots (apt/dnf/zypper) in one manifest

## Ansible post-install workflow

[`examples/ansible-workflow/`](../examples/ansible-workflow/) shows a complete pipeline that builds a Rocky compute image and then applies Ansible roles to it. See the [example's README](../examples/ansible-workflow/README.md).
