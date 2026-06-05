#!/bin/bash
# Test script for Ansible integration
# This script validates the example configuration

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="$SCRIPT_DIR/rocky-compute-ansible.yaml"

echo "==================================="
echo "Ansible Workflow Example Test"
echo "==================================="
echo

# Check if config file exists
if [ ! -f "$CONFIG_FILE" ]; then
    echo "ERROR: Config file not found: $CONFIG_FILE"
    exit 1
fi

echo "✓ Config file found: $CONFIG_FILE"
echo

# Validate directory structure
echo "Checking directory structure..."
required_files=(
    "playbooks/compute.yaml"
    "inventory/hosts"
    "inventory/group_vars/compute.yaml"
    "roles/chrony/tasks/main.yaml"
    "roles/chrony/templates/chrony.conf.j2"
    "roles/chrony/handlers/main.yaml"
)

for file in "${required_files[@]}"; do
    if [ -f "$SCRIPT_DIR/$file" ]; then
        echo "  ✓ $file"
    else
        echo "  ✗ $file (MISSING)"
        exit 1
    fi
done

echo
echo "All required files present!"
echo

# Show configuration summary
echo "Configuration Summary:"
echo "====================="
echo "Base Image: docker://rockylinux:9"
echo "Playbook: ./playbooks/compute.yaml"
echo "Inventory: ./inventory/"
echo "Groups: compute"
echo "Extra Vars:"
echo "  - ntp_server: time.example.com"
echo "  - datacenter: dc1"
echo "Verbose: 1"
echo

# Instructions for building
echo "To build this image, run:"
echo "  image-build build $CONFIG_FILE"
echo
echo "Or with the binary in your path:"
echo "  ./image-build build examples/ansible-workflow/rocky-compute-ansible.yaml"
echo

echo "==================================="
echo "Validation Complete!"
echo "==================================="
