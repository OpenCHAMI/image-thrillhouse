# image-thrillhouse

A Go-based image builder that wraps `buildah` to create layered OS images with support for multiple package managers. It is the next-generation replacement for the Python-based image-builder tool used by [OpenCHAMI](https://openchami.org/).

## Features

- **Multiple package managers** — DNF, Zypper, APT (parent builds only), mmdebstrap (scratch builds only)
- **Scratch & parent builds** — build from `scratch` or layer on top of an existing image
- **Declarative YAML config** with a `validate` subcommand
- **Multiple publishers** — local container storage, SquashFS, container registry, S3
- **OpenSCAP scanning** — XCCDF benchmarks + OVAL vulnerability evaluation
- **Structured logging** — JSON or text, configurable levels

## Install

### Container (recommended)

The pre-built unified image includes every supported package manager.

```bash
podman pull ghcr.io/openchami/image-thrillhouse:latest
```

See [docs/container-usage.md](docs/container-usage.md) for the full `podman run` invocation and flag explanations.

### RPM (RHEL / Rocky / AlmaLinux / Fedora)

Grab the `.rpm` for your architecture from the [latest release](https://github.com/OpenCHAMI/image-thrillhouse/releases/latest) and install it:

```bash
sudo dnf install ./image-thrillhouse-<version>.<arch>.rpm
```

`buildah` is a hard dependency; `squashfs-tools` and `podman` are recommended/suggested.

### Debian package (Debian / Ubuntu)

Grab the `.deb` for your architecture from the [latest release](https://github.com/OpenCHAMI/image-thrillhouse/releases/latest) and install it:

```bash
sudo apt install ./image-thrillhouse_<version>_<arch>.deb
```

`buildah` is a hard dependency; `squashfs-tools` is recommended and `podman` is suggested.

### From source

For development or unsupported platforms, see [docs/development.md](docs/development.md#building-from-source).

## Quick start

Save the following as `rocky-base.yaml`:

```yaml
meta:
  name: rocky-base
  tags:
    - "9.5"
  from: scratch

layer:
  manager:
    name: dnf
    options:
      releasever: "9"            # required for DNF scratch builds
    config: |
      [main]
      gpgcheck=1
      reposdir=/etc/image-thrillhouse/yum.repos.d
  repos:
    - path: /etc/image-thrillhouse/yum.repos.d/rocky-baseos.repo
      content: |
        [rocky-baseos]
        name=rocky-baseos
        baseurl=https://dl.rockylinux.org/pub/rocky/9/BaseOS/x86_64/os
        enabled=1
        gpgcheck=1
        gpgkey=https://dl.rockylinux.org/pub/rocky/RPM-GPG-KEY-Rocky-9
  actions:
    install:
      packages:
        - kernel
        - systemd

publish:
  - type: local
```

Validate, then build:

```bash
image-thrillhouse validate --config rocky-base.yaml
image-thrillhouse build --config rocky-base.yaml --log-level info
```

`validate` checks YAML syntax, required fields, backend option names/values, and publisher config — useful in CI before a full build.

## A second example: SquashFS output

The `publish` section is a list, so you can fan out to multiple targets in one build. To produce a bootable SquashFS file alongside the local image:

```yaml
publish:
  - type: local
  - type: squashfs
    path: /output/images        # writes <meta.name>-<meta.tags[0]>.squashfs
```

For registry and S3 targets, see [docs/configuration.md#publish](docs/configuration.md#publish).

## Command-line usage

```bash
image-thrillhouse build    --config <path>   # build an image
image-thrillhouse validate --config <path>   # validate config without building
image-thrillhouse version                    # print version info
```

Global flags:

- `--log-level` — `debug` | `info` | `warn` | `error` (default `info`)
- `--log-format` — `json` | `text` (default `json`)

## Recommendations

- **Start with `validate`.** Run `image-thrillhouse validate` in CI before any build — it catches typos in backend options and missing required fields before you pay for a long package download.
- **Use the unified container.** It already has DNF, Zypper, APT, and mmdebstrap. Switching distros is a config change, not an image change. See [docs/container-usage.md](docs/container-usage.md#why-a-unified-image).
- **Always set `releasever` for DNF scratch builds.** This is the most common scratch-build failure mode. See [docs/configuration.md](docs/configuration.md#backend-options).
- **Use `remove_packages` to slim images.** Drop debug packages, docs, and unused firmware in the same step that installs the base — see [docs/configuration.md#package-removal](docs/configuration.md#package-removal).
- **Pin image tags in production.** Use `ghcr.io/openchami/image-thrillhouse:v0.1.0` rather than `:latest` for reproducible builds.

## Documentation

- [Configuration reference](docs/configuration.md) — every field in the YAML, with examples
- [Container usage](docs/container-usage.md) — running the pre-built image, flag explanations, multi-version DNF
- [Package manager support](docs/package-managers.md) — backend feature matrix
- [Example configs](docs/examples.md) — annotated index of `tests/` configs
- [Development](docs/development.md) — building, testing, architecture, adding backends/publishers
- [Troubleshooting](docs/troubleshooting.md) — common errors
- [Migration from the Python image-builder](docs/migration-from-python.md)
- [Unit testing guide](TESTING.md)

## Related projects

- [Buildah](https://buildah.io/) — container building tool
- [Podman](https://podman.io/) — container runtime
- [OpenCHAMI](https://openchami.org/) — HPC cluster management (original use case)

## Contributing

Issues and PRs are welcome. Areas of interest include additional package managers (pacman, apk, …), additional publishers (Azure, GCP, …), broader test coverage, and build-time optimization.

## License

See [LICENSE](LICENSE).

## LLM use acknowledgement

The majority of the documentation and some of the code was written by Claude Sonnet 4.5 (training cutoff September 29, 2025). All generated content has been verified and tested.
