# Image Builder Config Converter

A standalone Python tool to convert image-builder (old format) YAML configuration files to image-thrillhouse (new format) configuration files.

## Features

- **Automatic field mapping**: Converts all standard fields from old to new format
- **Repository conversion**: Generates inline repository file content from alias/url/gpg fields
- **Publish configuration**: Converts multiple publish options to unified publish array
- **Ansible detection**: Detects ansible layer types and provides conversion guidance
- **Multi-line formatting**: Uses YAML literal block style for clean, readable repo and script content
- **Warning system**: Alerts on non-mappable fields or configurations requiring manual review
- **Flexible output**: Print to stdout or write to file
- **Dry-run mode**: Preview conversions without writing files
- **Verbose logging**: Track conversion progress step-by-step

## Requirements

- Python 3.6 or higher
- PyYAML library

Install PyYAML:
```bash
pip install pyyaml
# or
pip3 install pyyaml
```

## Usage

### Basic Usage

Convert a config file and print to stdout:
```bash
./convert-config.py old-config.yaml
```

Convert and write to a new file:
```bash
./convert-config.py old-config.yaml -o new-config.yaml
```

### Advanced Options

Preview conversion without writing (dry-run):
```bash
./convert-config.py old-config.yaml --dry-run
```

Enable verbose output to see conversion progress:
```bash
./convert-config.py old-config.yaml -o new-config.yaml --verbose
```

View help:
```bash
./convert-config.py --help
```

## Conversion Examples

### Example 1: Basic DNF/Rocky Config

**Old format (image-builder):**
```yaml
options:
  layer_type: 'base'
  name: 'rocky9-base'
  publish_tags: '9'
  pkg_manager: 'dnf'
  parent: 'scratch'
  publish_registry: 'registry.example.com'

repos:
  - alias: 'Rock_BaseOS'
    url: 'https://download.rockylinux.org/pub/rocky/9/BaseOS/x86_64/os/'
    gpg: 'https://dl.rockylinux.org/pub/rocky/RPM-GPG-KEY-Rocky-9'

packages:
  - kernel
  - wget

cmds:
  - cmd: 'echo "Build complete"'
```

**New format (image-thrillhouse):**
```yaml
meta:
  name: rocky9-base
  from: scratch
  tags:
  - '9'
layer:
  manager:
    name: dnf
  repos:
  - path: /etc/image-build/yum.repos.d/rock_baseos.repo
    content: |-
      [rock_baseos]
      name=Rock_BaseOS
      baseurl=https://download.rockylinux.org/pub/rocky/9/BaseOS/x86_64/os/
      enabled=1
      gpgcheck=1
      gpgkey=https://dl.rockylinux.org/pub/rocky/RPM-GPG-KEY-Rocky-9
    gpg: https://dl.rockylinux.org/pub/rocky/RPM-GPG-KEY-Rocky-9
  actions:
    install:
      packages:
      - kernel
      - wget
    commands:
    - run: echo "Build complete"
publish:
- type: registry
  url: registry.example.com
```

### Example 2: Config with Multiple Publish Targets

**Old format:**
```yaml
options:
  name: 'my-image'
  publish_local: true
  publish_registry: 'registry.example.com'
  registry_opts_push:
    - '--tls-verify=false'
```

**New format:**
```yaml
publish:
- type: local
- type: registry
  url: registry.example.com
  tls-verify: false
```

## Field Mapping Reference

| Old Format (image-builder) | New Format (image-thrillhouse) | Notes |
|----------------------------|-------------------------------|-------|
| `options.name` | `meta.name` | Direct mapping |
| `options.publish_tags` | `meta.tags` | String → array conversion |
| `options.parent` | `meta.from` | Adds `docker://` prefix if needed |
| `options.pkg_manager` | `layer.manager.name` | Nested under manager |
| `options.registry_opts_pull` | `meta.from-tls-verify` | Extracts TLS settings |
| `repos[].alias` | Generated repo filename | Used in path generation |
| `repos[].url` | `repos[].content` | Generates inline repo file |
| `repos[].gpg` | `repos[].gpg` | Preserved |
| `package_groups` | `layer.actions.install.groups` | Nested under actions |
| `packages` | `layer.actions.install.packages` | Nested under actions |
| `remove_packages` | `layer.actions.install.remove_packages` | Nested under actions |
| `copyfiles` | `layer.files` | Field rename + structure change |
| `cmds[].cmd` | `layer.actions.commands[].run` | Field rename |
| `options.publish_local` | `publish[].type: local` | Array entry |
| `options.publish_registry` | `publish[].type: registry` | Array entry with URL |

## Ansible Layer Type Handling

The image-thrillhouse format does not currently support ansible layer types. When the converter detects an ansible layer, it:

1. **Warns** the user that ansible is not supported
2. **Creates** a placeholder script command with TODO comments
3. **Preserves** the original playbook, inventory, and group information in comments

**Example ansible conversion:**
```yaml
layer:
  manager:
    name: dnf
  actions:
    commands:
    - script: |
        #!/bin/bash
        # TODO: Convert ansible playbook to shell commands
        # Original playbook: /data/configs/playbooks/compute.yaml
        # Original inventory: /data/configs/inventory/
        # Original groups: ['compute']
        # Add your shell commands here
        echo "Ansible conversion required"
```

**Manual steps required:**
1. Review the original ansible playbook
2. Convert ansible tasks to shell commands or scripts
3. Replace the placeholder script with actual commands
4. Test the converted configuration

## Warnings and Manual Review

The converter may issue warnings for:

- **Ansible layer types**: Requires manual conversion to commands/scripts
- **APT/Debian repositories**: Basic conversion; may need manual adjustment
- **Missing required fields**: Provides sensible defaults with warnings
- **Parent image format**: Converts to `docker://` format with notification

Always review warnings and verify the converted configuration before use.

## Limitations

1. **Ansible playbooks**: Cannot automatically convert ansible tasks to shell commands
2. **APT repositories**: Simple conversion; complex APT sources may need adjustment
3. **Custom options**: Package manager-specific options may need manual configuration
4. **Comments**: Original YAML comments are not preserved
5. **Complex logic**: Advanced bash logic in commands is preserved but not validated

## Testing the Converted Config

After conversion, validate the new config with image-thrillhouse:

```bash
# Test build with converted config
image-build build new-config.yaml

# Or validate the YAML structure
python3 -c "import yaml; yaml.safe_load(open('new-config.yaml'))"
```

## Troubleshooting

### PyYAML not found
```bash
pip3 install pyyaml
```

### Permission denied
```bash
chmod +x convert-config.py
```

### YAML syntax error in output
- Check for special characters in strings that may need escaping
- Validate the input YAML is correctly formatted
- Review warnings for potential issues

## Contributing

Improvements and bug fixes are welcome! Areas for enhancement:

- More sophisticated APT repository conversion
- Ansible task → shell command conversion heuristics
- YAML comment preservation
- Batch processing for multiple files
- Configuration validation against image-thrillhouse schema

## License

MIT License - Part of the OpenCHAMI project

## Support

For issues or questions:
- File an issue on the image-thrillhouse GitHub repository
- Join the OpenCHAMI community discussions
