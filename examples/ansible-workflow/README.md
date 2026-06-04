# Ansible Workflow Example

This example demonstrates using Ansible playbooks to configure container images.

## Features

- **Host-based Ansible**: Runs on the host system (not in container)
- **Auto-inventory**: Container automatically added to specified groups
- **Buildah plugin**: Uses `containers.podman.buildah` connection
- **No Ansible in image**: Smaller, cleaner images

## Prerequisites

### Install Ansible and Buildah Plugin

```bash
# Install Ansible
sudo dnf install ansible-core  # RHEL/Rocky/Fedora
# OR
sudo apt install ansible-core  # Debian/Ubuntu

# Install Buildah connection plugin
ansible-galaxy collection install containers.podman

# Verify installation
ansible-doc -t connection containers.podman.buildah
```

## Directory Structure

```
ansible-workflow/
├── compute-ansible.yaml           # image-build config
├── playbooks/
│   └── compute.yaml              # Ansible playbook
├── inventory/
│   ├── hosts                     # Optional static hosts
│   └── group_vars/
│       └── compute/
│           └── vars.yaml         # Group variables
└── roles/
    └── chrony/
        ├── tasks/main.yaml       # Chrony installation/config
        ├── templates/
        │   └── chrony.conf.j2    # Config template
        └── handlers/main.yaml    # Service handlers
```

## How It Works

### 1. Container Creation

The build starts with a Rocky Linux 9 base image and installs Python (required for Ansible modules).

### 2. Dynamic Inventory Generation

When you specify `groups: [compute]`, the tool generates:

```yaml
all:
  children:
    compute:
      hosts:
        rocky-compute-ansible-container:
          ansible_connection: containers.podman.buildah
```

### 3. Ansible Execution

The tool runs on the host:

```bash
ansible-playbook \
  -i inventory/ \
  -i <(dynamic inventory) \
  --limit rocky-compute-ansible-container \
  -v \
  -e ntp_server=time.example.com \
  -e datacenter=dc1 \
  playbooks/compute.yaml
```

### 4. Playbook Tasks

The playbook:
- Installs and configures chrony (via role)
- Installs compute packages (kernel, hwloc, openmpi)
- Creates application directories
- Writes configuration files

### 5. Image Publishing

The configured container is committed and published locally.

## Usage

### Build the Image

```bash
cd examples/ansible-workflow
image-build --config compute-ansible.yaml
```

### Expected Output

```
INFO Starting build name=rocky-compute-ansible
INFO Installing packages count=2
INFO Starting Run Commands count=1
INFO Starting Ansible playbook execution (host-based) playbook=./playbooks/compute.yaml groups=[compute]
DEBUG Generated dynamic inventory container=rocky-compute-ansible-xxx groups=[compute]

PLAY [Configure compute node] ******************************************

TASK [Gathering Facts] *************************************************
ok: [rocky-compute-ansible-xxx]

TASK [chrony : Install chrony] *****************************************
changed: [rocky-compute-ansible-xxx]

TASK [chrony : Configure chrony] ***************************************
changed: [rocky-compute-ansible-xxx]

TASK [chrony : Enable chronyd service] *********************************
changed: [rocky-compute-ansible-xxx]

TASK [Install compute packages] ****************************************
changed: [rocky-compute-ansible-xxx]

TASK [Create application directories] **********************************
changed: [rocky-compute-ansible-xxx] => (item=/opt/compute)
changed: [rocky-compute-ansible-xxx] => (item=/var/log/compute)

TASK [Create application config] ***************************************
changed: [rocky-compute-ansible-xxx]

TASK [Display configuration] *******************************************
ok: [rocky-compute-ansible-xxx] => {
    "msg": "Compute node configured for datacenter dc1"
}

PLAY RECAP *************************************************************
rocky-compute-ansible-xxx : ok=8    changed=6    unreachable=0    failed=0

INFO Ansible playbook execution completed successfully
INFO Publishing to local storage
INFO Build complete
```

### Verify the Image

```bash
# List built images
buildah images | grep rocky-compute-ansible

# Inspect the image
buildah run rocky-compute-ansible-9-ansible-v1.0.0 cat /opt/compute/config
# Output:
# DATACENTER=dc1
# NTP_SERVER=time.example.com

# Check installed packages
buildah run rocky-compute-ansible-9-ansible-v1.0.0 rpm -q kernel hwloc openmpi

# Verify chrony configuration
buildah run rocky-compute-ansible-9-ansible-v1.0.0 cat /etc/chrony.conf
```

## Customization

### Change NTP Server

Edit `compute-ansible.yaml`:

```yaml
commands:
  - ansible:
      extra_vars:
        ntp_server: "ntp.mycompany.com"
```

### Add More Groups

```yaml
commands:
  - ansible:
      groups:
        - compute
        - production
        - gpu_nodes
```

### Run Specific Tags

```yaml
commands:
  - ansible:
      playbook: ./playbooks/compute.yaml
      groups: [compute]
      tags: "install,configure"
      skip_tags: "debug"
```

### Increase Verbosity

```yaml
commands:
  - ansible:
      verbose: 4  # -vvvv
```

## Troubleshooting

### Error: ansible-playbook not found

```bash
sudo dnf install ansible-core
```

### Error: buildah connection plugin not found

```bash
ansible-galaxy collection install containers.podman
ansible-doc -t connection containers.podman.buildah
```

### Error: Python not found in container

The config already installs Python. If you remove it, Ansible modules won't work.

Ensure this is in your config:

```yaml
layer:
  actions:
    install:
      packages:
        - python3
        - python3-dnf
```

### Debug Ansible Connection

Run with maximum verbosity:

```yaml
commands:
  - ansible:
      verbose: 4
```

Or test the plugin manually:

```bash
# Create a test container
buildah from --name test-container rockylinux:9

# Test Ansible connection
ansible -i "test-container," all \
  -c containers.podman.buildah \
  -m ping

# Cleanup
buildah rm test-container
```

## Comparison to Python Version

### Python image-builder

```yaml
options:
  layer_type: 'ansible'
  name: 'compute-ansible'
  parent: 'base:v1'
  publish_local: true
  groups: ['compute']
  playbooks: '/data/playbooks/compute.yaml'
  inventory: '/data/inventory/'
```

### Go image-thrillhouse (This)

```yaml
meta:
  name: compute-ansible
  from: localhost/base:v1
  tags: ["v1"]

layer:
  manager:
    name: dnf
  actions:
    install:
      packages: [python3, python3-dnf]
    commands:
      - ansible:
          playbook: ./playbooks/compute.yaml
          inventory: ./inventory/
          groups: [compute]

publish:
  - type: local
```

**Key Differences:**
- Must explicitly install Python
- `layer_type` → `commands.ansible`
- More explicit configuration structure
- Can mix Ansible with other commands

## Next Steps

- Add more roles to `roles/`
- Create multi-stage builds with multiple playbooks
- Use Ansible Vault for secrets
- Add integration tests
- Publish to registry or S3

## See Also

- [Ansible Support Documentation](../../ANSIBLE_SUPPORT.md)
- [Buildah Connection Plugin Docs](https://docs.ansible.com/ansible/latest/collections/containers/podman/buildah_connection.html)
- [Main README](../../README.md)
