# Migration from the Python Image Builder

This Go tool is a rewrite of the Python-based `image-builder` previously used by OpenCHAMI.

## Configuration format

**Python** uses a flatter structure:

```yaml
options:
  layer_type: base
  name: my-image
  pkg_manager: dnf
  parent: scratch
  publish_local: true
```

**Go** uses a structured format:

```yaml
meta:
  name: my-image
  tags:
    - latest
  from: scratch
layer:
  manager:
    name: dnf
publish:
  - type: local
```

See [configuration.md](configuration.md) for the full Go format.

## Feature parity

- ✅ Base layer builds
- ✅ Multiple package managers (expanded: dnf, zypper, apt, mmdebstrap)
- ✅ Local publishing
- ✅ SquashFS publishing
- ✅ Registry publishing
- ✅ S3 publishing with kernel/initramfs extraction
- ✅ OpenSCAP security scanning (new)
- ✅ Package removal (new)
- ✅ GPG key import for repositories (new)
- ✅ Recursive host-directory copies (`layer.directories`, new)
- ✅ Image labels/metadata
- ✅ Ansible playbooks as a build command (in-container `ansible:` command; new). A post-build workflow is also supported — see [`examples/ansible-workflow/`](../examples/ansible-workflow/)
- ✅ Multi-arch manifests + `promote` release tagging (new; no Python equivalent)
