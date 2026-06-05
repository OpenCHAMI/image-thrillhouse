# Ansible Workflow Example

This example demonstrates how to use Ansible playbooks within the image-build tool to configure container images.

## Overview

This example creates a Rocky Linux 9 compute node image with Ansible-based configuration. The Ansible playbook:
- Configures NTP time synchronization using chrony
- Sets up datacenter-specific configuration
- Creates a configuration marker file

## Directory Structure

```
.
├── rocky-compute-ansible.yaml     # Main image-build config
├── inventory/
│   ├── group_vars/
│   │   └── compute.yaml          # Group variables
│   └── hosts                      # Static inventory (optional)
├── playbooks/
│   └── compute.yaml               # Main playbook
└── roles/
    └── chrony/
        ├── tasks/
        │   └── main.yaml          # Chrony installation tasks
        ├── templates/
        │   └── chrony.conf.j2     # Chrony config template
        └── handlers/
            └── main.yaml          # Service restart handlers
```

**Note:** The `inventory/hosts` file is optional. The tool will automatically generate a `localhost` inventory file with the groups specified in your config.

## How It Works

### 1. Ansible Installation

The config file installs the necessary packages in the container:
```yaml
actions:
  install:
    packages:
      - ansible-core    # Ansible itself
      - python3         # Python interpreter
      - python3-dnf     # Required for Ansible dnf module
```

### 2. Ansible Command Execution

The Ansible command is defined in the `commands` section:
```yaml
commands:
  - ansible:
      playbook: ./playbooks/compute.yaml
      inventory: ./inventory/
      roles: ./roles              # Optional: defaults to "roles"
      groups:
        - compute
      extra_vars:
        ntp_server: "time.example.com"
        datacenter: "dc1"
      verbose: 1
```

### 3. What Happens During Build

When the build runs, the tool:

1. **Verifies Ansible** is installed in the container
2. **Creates temporary directory structure** in `/tmp/image-build-ansible/`:
   ```
   /tmp/image-build-ansible/
   ├── ansible.cfg
   ├── inventory/
   │   ├── group_vars/
   │   ├── hosts
   │   └── 00-generated-localhost  # Auto-generated, sorts first
   ├── playbooks/
   │   └── compute.yaml
   └── roles/
       └── chrony/
   ```
3. **Copies files**:
   - Playbook from `./playbooks/compute.yaml` → container
   - Inventory from `./inventory/` → container (if specified)
   - Roles from `./roles/` → container (defaults to `roles` if not specified)
4. **Generates files**:
   - `ansible.cfg` with `roles_path = /tmp/image-build-ansible/roles` (absolute path)
   - `00-generated-localhost` inventory file with group assignments (prefix ensures it's read first)
5. **Executes** `ANSIBLE_CONFIG=/tmp/image-build-ansible/ansible.cfg ansible-playbook -i /tmp/image-build-ansible/inventory/00-generated-localhost -i /tmp/image-build-ansible/inventory /tmp/image-build-ansible/playbooks/compute.yaml`
6. **Cleans up** temporary files after execution

### 4. Dynamic Localhost Inventory

The tool automatically generates a `00-generated-localhost` inventory file (without extension, following Ansible conventions). The `00-` prefix ensures it's read first in alphanumeric order:

```ini
[compute]
localhost ansible_connection=local
```

This ensures the playbook runs against localhost within the container, and localhost is assigned to the specified groups. The file is explicitly specified first with `-i` to guarantee it's read before any user-provided inventory files.

## Configuration Options

### Required Fields

- `playbook`: Path to the Ansible playbook (relative to config file)
- `groups`: List of groups to assign localhost to

### Optional Fields

- `inventory`: Path to inventory directory or file (optional)
- `roles`: Path to roles directory (optional, defaults to `roles` relative to config file)
- `extra_vars`: Dictionary of extra variables to pass with `-e`
- `tags`: Ansible tags to run (`--tags`)
- `skip_tags`: Ansible tags to skip (`--skip-tags`)
- `verbose`: Verbosity level 0-4 (maps to `-v`, `-vv`, `-vvv`, `-vvvv`)
- `check_mode`: Run in check mode (`--check`)

## Running the Example

```bash
# Build the image
image-build build examples/ansible-workflow/rocky-compute-ansible.yaml

# The resulting image will be available locally
podman images | grep rocky-compute-ansible
```

## Customization

### Using Different NTP Servers

Modify the `extra_vars` in the config:
```yaml
extra_vars:
  ntp_server: "pool.ntp.org"
  datacenter: "dc2"
```

### Adding More Roles

1. Create role directory under `roles/`
2. Add role tasks in `roles/<rolename>/tasks/main.yaml`
3. Include role in playbook:
   ```yaml
   - name: Apply new role
     ansible.builtin.include_role:
       name: myrole
   ```

### Using Custom Roles Path

If your roles are in a different location:
```yaml
- ansible:
    playbook: ./playbooks/compute.yaml
    roles: /path/to/my/roles  # Can be absolute or relative to config
    groups:
      - compute
```

The default is `roles` (relative to the config file location).

### Using Tags

Control which tasks run with tags:
```yaml
- ansible:
    playbook: ./playbooks/compute.yaml
    groups:
      - compute
    tags: "chrony,network"  # Only run tasks tagged with these
```

### Check Mode (Dry Run)

Test without making changes:
```yaml
- ansible:
    playbook: ./playbooks/compute.yaml
    groups:
      - compute
    check_mode: true
```

## Benefits of Ansible Integration

1. **Reusable Configuration**: Use existing Ansible roles and playbooks
2. **Complex Logic**: Leverage Ansible's templating and conditionals
3. **Idempotent**: Ansible ensures consistent state regardless of base image
4. **Ecosystem**: Access thousands of community roles from Ansible Galaxy
5. **Testing**: Use `check_mode` to validate changes before applying

## Troubleshooting

### Ansible Not Found
Ensure `ansible-core` or `ansible` is installed in the `install.packages` section.

### Module Not Found
Install required Python packages:
```yaml
packages:
  - ansible-core
  - python3-dnf      # For dnf module
  - python3-libselinux  # For SELinux modules
```

### Verbosity for Debugging
Increase verbosity to see more details:
```yaml
verbose: 3  # Very verbose output
```

### Path Issues
All paths are relative to the config file location:
```yaml
playbook: ./playbooks/compute.yaml  # Relative to config
inventory: /absolute/path/inventory  # Or absolute
```

## Advanced Examples

### Multiple Playbooks

Run multiple playbooks in sequence:
```yaml
commands:
  - ansible:
      playbook: ./playbooks/base.yaml
      groups: [all]
  - ansible:
      playbook: ./playbooks/compute.yaml
      groups: [compute]
```

### Conditional Execution

Use Ansible conditionals for different scenarios:
```yaml
extra_vars:
  environment: "production"
  enable_monitoring: "true"
```

Then in your playbook:
```yaml
- name: Install monitoring (production only)
  ansible.builtin.dnf:
    name: node_exporter
    state: present
  when: environment == "production" and enable_monitoring | bool
```
