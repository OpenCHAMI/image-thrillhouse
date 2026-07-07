# Development

## Building from source

```bash
git clone https://github.com/OpenCHAMI/image-thrillhouse.git
cd image-thrillhouse
go build -o image-thrillhouse ./cmd/image-thrillhouse
sudo mv image-thrillhouse /usr/local/bin/
```

### Build prerequisites

- Go 1.26 or later (see `go.mod`)
- Buildah
- Root or rootless container capabilities
- `mksquashfs` (for SquashFS publishing)
- C toolchain + `pkg-config` + `libgpgme-dev` + `libbtrfs-dev` + `libdevmapper-dev` (transitive cgo deps via `containers/storage`)

On macOS the cgo deps are not available, so the binary builds and the tests in `internal/buildah` only work on Linux. The pure-Go packages test fine anywhere.

**Tip — compiling and testing without gpgme:** the `containers_image_openpgp` build tag swaps containers/image's gpgme signature backend for a pure-Go one, which removes the `pkg-config`/`libgpgme-dev` requirement entirely. On machines without the cgo deps (macOS, minimal CI runners) the *entire* module compiles and tests with:

```bash
go build -tags containers_image_openpgp ./...
go test  -tags containers_image_openpgp ./...
```

**Version stamping:** `image-thrillhouse version` reports the value injected at build time via `-ldflags "-X main.version=…"`. `make build`, the RPM spec, and `debian/rules` all pass their own version through; a bare `go build` produces a binary that reports `dev`.

### Build a local container

```bash
podman build -t image-thrillhouse:dev -f Dockerfile .
```

## Unit tests

```bash
go test ./...
go test -v ./...          # Verbose output
go test -cover ./...      # With coverage
```

See [`TESTING.md`](../TESTING.md) for the full unit-testing guide.

**Caveat.** `internal/buildah` depends transitively on cgo bindings (gpgme, btrfs, devicemapper) via `containers/storage`. On systems without those libraries — notably macOS — `go test ./...` will fail to build that package. Add `-tags containers_image_openpgp` (see the build tip above) to run the full suite anyway.

### Unit test coverage

- ✅ `internal/config` — YAML parsing, schema validation, var merging
- ✅ `internal/labels` — OCI label generation
- ✅ `internal/backend/apt`
- ✅ `internal/backend/dnf`
- ✅ `internal/backend/zypper` (including informational exit codes 102/103/107 and global-vs-subcommand flag placement)
- ✅ `internal/backend/mmdebstrap`
- ✅ `internal/builder` — helpers plus fake-container tests (label application before publish, empty-root install ordering)
- ✅ `internal/manifest`, `internal/tag` — DAG resolution, render-based deterministic tag hashing
- ✅ `internal/container`, `internal/fetch`, `internal/oscap`, `internal/publisher/*`
- ⚠️ `internal/buildah` — covered only by the integration suite below

## Integration tests

[`tests/`](../tests/) holds shell-driven smoke tests that build a unified container image and exercise each backend against real distro repos. The top-level [`run-all-tests.sh`](../run-all-tests.sh) runs everything in sequence, or in parallel when asked:

```bash
./run-all-tests.sh                  # all backends, sequential
./run-all-tests.sh --parallel       # scratch tests in parallel
./run-all-tests.sh --dnf            # only DNF
./run-all-tests.sh --apt            # only APT / mmdebstrap
./run-all-tests.sh --zypper         # only Zypper
```

Each backend script (`tests/<backend>/test-<backend>-{scratch,parent}.sh`) can also be run directly. All scripts share a single podman image tag, `image-thrillhouse:test`, built from the repo's `Dockerfile`.

### Rebuilding the test image after a code change

The container guard skips the build if `image-thrillhouse:test` already exists, so iterating on the Go binary requires forcing a rebuild:

```bash
REBUILD_IMAGE=1 ./run-all-tests.sh                    # rebuild and run everything
REBUILD_IMAGE=1 ./tests/zypper/test-zypper-scratch.sh # rebuild and run just one
```

Without `REBUILD_IMAGE=1` the test will silently reuse a stale binary — if a test "still fails" after a fix you believe should help, this is the first thing to check.

Per-test logs land in `test-output/<backend>-<type>/`.

## Architecture

```
image-thrillhouse/
├── cmd/image-thrillhouse/   # Main application entry point
│   └── main.go
├── internal/
│   ├── backend/             # Package manager implementations
│   │   ├── backend.go       # Backend interface
│   │   ├── dnf/
│   │   ├── zypper/
│   │   ├── apt/
│   │   └── mmdebstrap/
│   ├── buildah/             # Buildah container management
│   │   ├── container.go
│   │   └── store.go
│   ├── builder/             # Build orchestration
│   │   └── builder.go
│   ├── config/              # Configuration parsing & validation
│   │   ├── config.go
│   │   └── validate.go
│   ├── container/           # Container abstractions
│   │   ├── container.go
│   │   └── logwriter.go
│   ├── labels/              # OCI image label generation
│   │   └── labels.go
│   ├── oscap/               # OpenSCAP security scanning
│   │   └── oscap.go
│   └── publisher/           # Image publishing
│       ├── publisher.go     # Publisher interface
│       ├── local/
│       ├── squashfs/
│       ├── registry/
│       └── s3/
└── tests/                   # Example configurations + integration tests
```

## Adding a new backend

1. Create a new package in `internal/backend/mymanager/`
2. Implement the `Backend` interface from `internal/backend/backend.go`
3. Register it in `cmd/image-thrillhouse/main.go` in `newBackend()`

## Adding a new publisher

1. Create a new package in `internal/publisher/mypublisher/`
2. Implement the `Publisher` interface from `internal/publisher/publisher.go`
3. Register it in `cmd/image-thrillhouse/main.go` in `newPublishers()`

## Building distribution packages locally

The CI in [`.github/workflows/build-deb.yml`](../.github/workflows/build-deb.yml) is the source of truth for release builds, but you can also build locally.

**Debian / Ubuntu** — see [`debian/README.md`](../debian/README.md) for full instructions:

```bash
make deb
# or
dpkg-buildpackage -us -uc -b
```

**RPM (RHEL / Rocky / Fedora):**

```bash
make rpm
```

**Container image:**

```bash
podman build -t image-thrillhouse:dev -f Dockerfile .
```
