<!--
SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC

SPDX-License-Identifier: MIT
-->

# Promoting a release tag

`image-thrillhouse promote` gives an already-built, already-tested image a
human-readable release tag — e.g. `release-0.0.1` — **without rebuilding it**.

Manifest builds publish images to an OCI registry under a content-addressed tag
(the layer's deterministic hash; see [Manifests](configuration.md#manifests)).
That tag is perfect for caching and reproducibility but not for humans. `promote`
takes the tested content-tagged image and gives it a release tag. It has two
targets:

- **`--to registry`** (default) — retag within the registry: copy the
  content-tagged image to the release tag. Blobs already exist, so only a new tag
  is written; it points at the *exact bytes* that were tested.
- **`--to s3`** — project the image into S3 boot artifacts (rootfs / kernel /
  initramfs) under the release tag, for network boot. Pulls the content-tagged
  image and re-extracts it — a re-package of tested bytes, never a rebuild.

The OCI registry is the source of truth in both cases. `promote` recomputes the
layer's content tag from the manifest, so it must run from the **same checkout**
that built the image (the hash covers the rendered config and referenced files).

## Usage

```
image-thrillhouse promote \
  --manifest <path> \
  [--layer <logical-name>] \
  [--arch <arch>] \
  --release <tag> \
  [--to registry|s3] \
  [--force] [--dry-run]
```

The source is always the layer's own `registry` [publish block](configuration.md#publish)
(URL + `meta.name`) — the layer must have one, or promote fails. For `--to s3`
the layer must also have an `s3` publish block (bucket + prefix).

### Which layers get promoted

**Omitting `--layer` promotes the whole manifest** — every layer that declares a
publish block of the target type. So which layers reach S3 is a *config*
decision, not something the pipeline enumerates: put a
[`promote-only`](configuration.md#promote-only) `s3` block on your release
targets, and `promote --to s3` picks exactly those up.

```
# one command releases every bootable image in the manifest, all arches:
image-thrillhouse promote --manifest manifests/rocky.yaml \
  --release test-release-1.2.3 --to s3
```

Layers with no matching block are skipped silently (visible at `--log-level debug`).
Naming `--layer` explicitly promotes just that one — and *errors* if it has no
block for the target, so a typo fails loud rather than doing nothing:

```
Error: layer "base" declares no "s3" publish block
```

Note `--to registry` matches every layer that has a registry block — which is
usually all of them — so a bare `promote --release X` tags the entire build set
in OCI. Use `--layer` if you only want specific images tagged.

## `--to registry` (retag)

Copies the content-tagged image to the release tag in the same repository.

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

```
# omit --arch to retag every arch:
image-thrillhouse promote --manifest manifests/rocky-multiarch.yaml \
  --layer rocky-base --release release-0.0.1
# rocky-base-x86_64:<tag>   →  :release-0.0.1
# rocky-base-aarch64:<tag>  →  :release-0.0.1

# or --arch to retag just one:
image-thrillhouse promote --manifest manifests/rocky-multiarch.yaml \
  --layer rocky-base --arch aarch64 --release release-0.0.1
```

All arches are resolved before anything is written, so a config error fails
before any tag is applied. If two arches would resolve to the **same** destination
reference — meaning the arch isn't part of `meta.name` — promote refuses rather
than silently overwriting one with the other.

## `--to s3` (materialize boot artifacts)

Projects the OCI image into S3 as the three files a node needs to network-boot,
laid out as a self-contained directory per tag:

```
<prefix><release>/<arch>/rootfs.squashfs
<prefix><release>/<arch>/vmlinuz
<prefix><release>/<arch>/initramfs.img
```

`promote` pulls the content-tagged image, mounts its rootfs, creates the SquashFS,
and extracts the kernel and initramfs — the same extraction the build-time S3
publisher uses, so build-time and promote produce identical layouts. Because
everything for a tag lives under one directory, a materialized release is
self-contained and immutable: a different tag is a different directory, with no
shared kernel-version-keyed object a later build could overwrite. The `<arch>`
segment is omitted for single-arch (non-manifest) builds.

```
image-thrillhouse promote \
  --manifest manifests/rocky-multiarch.yaml \
  --layer rocky-base \
  --release release-0.0.1 \
  --to s3
# → compute/release-0.0.1/x86_64/{rootfs.squashfs,vmlinuz,initramfs.img}
# → compute/release-0.0.1/aarch64/{rootfs.squashfs,vmlinuz,initramfs.img}
```

Multi-arch materializes each arch into its own `<arch>` segment, so there is no
collision — arches run sequentially. Pass `--arch` to materialize just one.

By default promote **fails if the release is already materialized** (probed with
a `HeadObject` on the rootfs key before the pull, so it's cheap):

```
Error: release "release-0.0.1" already materialized in s3 (use --force to overwrite)
```

Pass `--force` to re-materialize and overwrite the objects.

> Cross-arch note: materializing every arch in one invocation pulls and mounts
> each arch's image on the host running `promote`. If your environment can't pull
> a foreign-arch image, run `promote --to s3 --arch <arch>` on a host of that arch
> (each arch's build machine is a natural fit).

## Flags

| Flag | Description |
|------|-------------|
| `--manifest` | Manifest file (required). |
| `--layer` | Logical layer to promote. Omit to promote every layer declaring a block for `--to`. |
| `--arch` | Target arch for a multi-arch manifest. Omit to promote every arch. |
| `--release` | The release tag to write (required), e.g. `release-0.0.1`. |
| `--to` | `registry` (default, retag) or `s3` (materialize boot artifacts). |
| `--force` | Overwrite an existing release if it already exists (registry tag, or s3 objects); otherwise promote fails. |
| `--dry-run` | Resolve and print what would happen, without contacting the target. |
| `--var-file`, `--var` | Same var inputs as `build`, so the recomputed content tag matches what was built. |

## Authentication

- **Registry** (source pull, and `--to registry` push): set `REGISTRY_AUTH_FILE`
  to a containers-auth file (e.g. `podman login` output), or rely on the default
  containers/image credential search. TLS verification follows `tls-verify` on the
  layer's `registry` publish block.
- **S3** (`--to s3`): credentials come from the `S3_ACCESS` and `S3_SECRET`
  environment variables, same as the build-time S3 publisher.

## Notes and limitations

- **OCI is the source of truth.** Both targets read the content-tagged image from
  the registry. There is no S3→S3 or S3→registry path.
- **`--to registry` is a same-repo retag**, not a copy between repositories, and
  not a manifest list — multi-arch tags each arch's separate image.
- **Same checkout.** The content tag is recomputed from the manifest, so promote
  must see the same manifest, var files, and referenced content that produced the
  build.
