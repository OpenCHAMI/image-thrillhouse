<!--
SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC

SPDX-License-Identifier: MIT
-->

# Promoting a release tag

`image-thrillhouse promote` gives an already-built, already-tested image a
human-readable release tag — e.g. `release-0.0.1` — **without rebuilding it**.

Manifest builds publish images under a content-addressed tag (the layer's
deterministic hash; see [Manifests](configuration.md#manifests)). That tag is
perfect for caching and reproducibility but not for humans. `promote` copies the
content-tagged image to a release tag inside the same registry repository. Because
the blobs already exist there, only a new tag is written — the release tag points
at the *exact bytes* that were tested, never a fresh build.

## When to use it

The intended CI/CD flow:

1. **Build** each layer — it publishes to the registry under its content tag.
2. **Test** that content-tagged image.
3. **Promote** the tested content tag to a release tag.

```
build  →  registry.example/rocky-base:a1b2c3d4…   (content tag)
test   →  ✔
promote→  registry.example/rocky-base:release-0.0.1
```

## Usage

```
image-thrillhouse promote \
  --manifest <path> \
  --layer <logical-name> \
  [--arch <arch>] \
  --release <tag> \
  [--force] [--dry-run]
```

`promote` recomputes the layer's content tag from the manifest, so it must run
from the **same checkout** that built the image (the hash covers the rendered
config and referenced files). The image name and registry URL come from the
layer's own `registry` [publish block](configuration.md#publish) — the layer must
have one, or promote fails.

### Single image

```
image-thrillhouse promote \
  --manifest manifests/rocky.yaml \
  --layer rocky-base \
  --release release-0.0.1
# registry.example/rocky-base:<contentTag>  →  :release-0.0.1
```

### Multi-arch

Each arch of a multi-arch manifest builds into its **own repository** (the arch
is in `meta.name`, e.g. `rocky-base-x86_64` / `rocky-base-aarch64`) under its own
content tag. Promoting applies the same release-tag *string* to each arch's image
in its own repo — it does **not** build a combined manifest list.

- Omit `--arch` to retag **every** arch of the layer:

  ```
  image-thrillhouse promote \
    --manifest manifests/rocky-multiarch.yaml \
    --layer rocky-base \
    --release release-0.0.1
  # rocky-base-x86_64:<tag>   →  :release-0.0.1
  # rocky-base-aarch64:<tag>  →  :release-0.0.1
  ```

  All arches are resolved before anything is written, so a config error fails
  before any tag is applied. The retags then run one after another.

- Pass `--arch` to retag just one:

  ```
  image-thrillhouse promote --manifest manifests/rocky-multiarch.yaml \
    --layer rocky-base --arch aarch64 --release release-0.0.1
  ```

If two arches would resolve to the **same** destination reference — which means
the arch isn't part of `meta.name` — promote refuses rather than silently
overwriting one with the other. Put the arch in the name (e.g.
`name: rocky-base-{{ .arch }}`) so each arch has its own repo.

## Flags

| Flag | Description |
|------|-------------|
| `--manifest` | Manifest file (required). |
| `--layer` | Logical layer name to promote (required). |
| `--arch` | Target arch for a multi-arch manifest. Omit to retag every arch. |
| `--release` | The release tag to write (required), e.g. `release-0.0.1`. |
| `--force` | Overwrite the release tag if it already exists. Without it, promote fails when the tag is present. |
| `--dry-run` | Resolve and print what would be retagged, without contacting the registry. |
| `--var-file`, `--var` | Same var inputs as `build`, so the recomputed content tag matches what was built. |

### `--force`

By default promote **fails if the release tag already exists**, so a release is
never silently moved:

```
Error: release tag registry.example/rocky-base:release-0.0.1 already exists (use --force to overwrite)
```

Pass `--force` to move the tag to the new image.

### `--dry-run`

Resolves the content tag, source, and destination and prints them without any
network calls — no credentials required. Useful to confirm which images a
promotion would touch:

```
image-thrillhouse promote --manifest manifests/rocky-multiarch.yaml \
  --layer rocky-base --release release-0.0.1 --dry-run
```

## Authentication

Promote uses the same registry auth as `build`: set `REGISTRY_AUTH_FILE` to a
containers-auth file (e.g. the output of `podman login`), or rely on the default
containers/image credential search. TLS verification follows the `tls-verify`
setting on the layer's `registry` publish block.

## Notes and limitations

- **Registry only.** Promote retags images within an OCI registry. It does not
  promote to S3 or other targets.
- **Same repository.** The release tag is written to the same repo the content
  tag lives in; promote does not copy between repositories.
- **Not a manifest list.** Multi-arch promotion tags each arch's separate image;
  it does not assemble a combined multi-platform tag.
- **Same checkout.** The content tag is recomputed from the manifest, so promote
  must see the same manifest, var files, and referenced content that produced the
  build.
