#!/usr/bin/env python3
# SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
#
# SPDX-License-Identifier: MIT
"""
Image Builder Config Converter

Converts old image-builder YAML config files to new image-thrillhouse format.

Usage:
    ./convert-config.py input.yaml [-o output.yaml] [--dry-run] [--verbose]

Author: OpenCHAMI Project
License: MIT
"""

import argparse
import sys
import yaml
from typing import Dict, List, Any, Optional
from pathlib import Path


# Custom YAML representer for multi-line strings
def str_representer(dumper, data):
    """Use literal block style (|) for multi-line strings"""
    if '\n' in data:
        return dumper.represent_scalar('tag:yaml.org,2002:str', data, style='|')
    return dumper.represent_scalar('tag:yaml.org,2002:str', data)

yaml.add_representer(str, str_representer)


class ConversionWarning:
    """Represents a warning during conversion"""
    def __init__(self, message: str, field: Optional[str] = None):
        self.message = message
        self.field = field

    def __str__(self):
        if self.field:
            return f"WARNING [{self.field}]: {self.message}"
        return f"WARNING: {self.message}"


class ConfigConverter:
    """Converts image-builder configs to image-thrillhouse format"""

    def __init__(self, verbose: bool = False):
        self.verbose = verbose
        self.warnings: List[ConversionWarning] = []

    def log(self, message: str):
        """Log verbose messages"""
        if self.verbose:
            print(f"INFO: {message}", file=sys.stderr)

    def warn(self, message: str, field: Optional[str] = None):
        """Add a warning"""
        warning = ConversionWarning(message, field)
        self.warnings.append(warning)
        print(str(warning), file=sys.stderr)

    def convert(self, old_config: Dict[str, Any]) -> Dict[str, Any]:
        """Convert old config format to new format"""
        self.log("Starting conversion...")

        new_config = {}

        # Convert meta section
        new_config['meta'] = self._convert_meta(old_config.get('options', {}))

        # Convert layer section
        new_config['layer'] = self._convert_layer(old_config)

        # Convert publish section
        publish = self._convert_publish(old_config.get('options', {}))
        if publish:
            new_config['publish'] = publish

        self.log(f"Conversion complete with {len(self.warnings)} warnings")
        return new_config

    def _convert_meta(self, options: Dict[str, Any]) -> Dict[str, Any]:
        """Convert options section to meta section"""
        self.log("Converting meta section...")

        meta = {}

        # Name (required)
        if 'name' in options:
            meta['name'] = options['name']
        else:
            self.warn("No 'name' field found in options", "meta.name")
            meta['name'] = 'unnamed-image'

        # Parent/From image
        parent = options.get('parent', 'scratch')
        if parent == 'scratch':
            meta['from'] = 'scratch'
        elif parent.startswith('docker://'):
            meta['from'] = parent
        elif '/' in parent or ':' in parent:
            # Looks like a registry image
            meta['from'] = f"docker://{parent}"
        else:
            # Local image reference
            meta['from'] = f"docker://{parent}"
            self.warn(f"Converted parent '{parent}' to '{meta['from']}'", "meta.from")

        # Tags
        publish_tags = options.get('publish_tags', [])
        if isinstance(publish_tags, str):
            meta['tags'] = [publish_tags]
        elif isinstance(publish_tags, list):
            meta['tags'] = publish_tags
        else:
            meta['tags'] = []

        # TLS verify for pulling base image
        if 'registry_opts_pull' in options:
            if '--tls-verify=false' in options['registry_opts_pull']:
                meta['from-tls-verify'] = False

        return meta

    def _convert_layer(self, old_config: Dict[str, Any]) -> Dict[str, Any]:
        """Convert layer section"""
        self.log("Converting layer section...")

        options = old_config.get('options', {})
        layer = {}

        # Check for ansible layer type
        layer_type = options.get('layer_type', 'base')
        if layer_type == 'ansible':
            self.log("Detected ansible layer type - attempting conversion to commands")
            layer.update(self._convert_ansible_layer(old_config))
            return layer

        # Package manager
        layer['manager'] = self._convert_manager(options)

        # Repositories
        if 'repos' in old_config:
            layer['repos'] = self._convert_repos(
                old_config['repos'],
                options.get('pkg_manager', 'dnf')
            )

        # Actions (install, commands)
        layer['actions'] = self._convert_actions(old_config)

        # Files (copyfiles -> files)
        if 'copyfiles' in old_config:
            if 'files' not in layer:
                layer['files'] = []
            layer['files'].extend(self._convert_copyfiles(old_config['copyfiles']))

        return layer

    def _convert_manager(self, options: Dict[str, Any]) -> Dict[str, Any]:
        """Convert package manager configuration"""
        self.log("Converting package manager...")

        pkg_manager = options.get('pkg_manager', 'dnf')
        manager = {'name': pkg_manager}

        # Add manager-specific options if present
        # These would come from dnf/zypper/apt specific options in old format
        # For now, we'll keep it simple

        return manager

    def _convert_repos(self, repos: List[Dict[str, Any]], pkg_manager: str) -> List[Dict[str, Any]]:
        """Convert repository definitions"""
        self.log(f"Converting {len(repos)} repositories...")

        new_repos = []
        for i, repo in enumerate(repos):
            alias = repo.get('alias', f'repo-{i}')
            url = repo.get('url', '')
            gpg = repo.get('gpg', '')

            # Generate repo file path based on package manager
            if pkg_manager in ['dnf', 'yum']:
                repo_path = f"/etc/image-thrillhouse/yum.repos.d/{alias.lower()}.repo"
                content = self._generate_yum_repo_content(alias, url, gpg)
            elif pkg_manager == 'zypper':
                repo_path = f"/etc/zypp/repos.d/{alias.lower()}.repo"
                content = self._generate_zypper_repo_content(alias, url, gpg)
            elif pkg_manager == 'apt':
                repo_path = f"/etc/apt/sources.list.d/{alias.lower()}.list"
                content = self._generate_apt_repo_content(url)
                self.warn("APT repo conversion is basic - may need manual adjustment", f"repos[{i}]")
            else:
                self.warn(f"Unknown package manager '{pkg_manager}' - using dnf format", f"repos[{i}]")
                repo_path = f"/etc/image-thrillhouse/yum.repos.d/{alias.lower()}.repo"
                content = self._generate_yum_repo_content(alias, url, gpg)

            new_repos.append({
                'path': repo_path,
                'content': content
            })

            if gpg:
                new_repos[-1]['gpg'] = gpg

        return new_repos

    def _generate_yum_repo_content(self, alias: str, url: str, gpg: str = '') -> str:
        """Generate YUM/DNF repo file content"""
        lines = [
            f"[{alias.lower()}]",
            f"name={alias}",
            f"baseurl={url}",
            "enabled=1"
        ]

        if gpg:
            lines.extend([
                "gpgcheck=1",
                f"gpgkey={gpg}"
            ])
        else:
            lines.append("gpgcheck=0")

        return "\n".join(lines)

    def _generate_zypper_repo_content(self, alias: str, url: str, gpg: str = '') -> str:
        """Generate Zypper repo file content"""
        lines = [
            f"[{alias.lower()}]",
            f"name={alias}",
            f"baseurl={url}",
            "enabled=1",
            "gpgcheck=0"  # Zypper handles GPG differently
        ]

        return "\n".join(lines)

    def _generate_apt_repo_content(self, url: str) -> str:
        """Generate APT sources.list content"""
        # This is a simplified conversion - APT repos have complex syntax
        return f"deb {url} stable main\n"

    def _convert_actions(self, old_config: Dict[str, Any]) -> Dict[str, Any]:
        """Convert actions section (install, commands)"""
        self.log("Converting actions...")

        actions = {}

        # Install section
        install = {}

        if 'packages' in old_config:
            install['packages'] = old_config['packages']

        if 'package_groups' in old_config:
            install['groups'] = old_config['package_groups']

        if 'remove_packages' in old_config:
            install['remove_packages'] = old_config['remove_packages']

        if install:
            actions['install'] = install

        # Commands
        if 'cmds' in old_config:
            actions['commands'] = self._convert_commands(old_config['cmds'])

        return actions

    def _convert_commands(self, cmds: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
        """Convert commands section"""
        self.log(f"Converting {len(cmds)} commands...")

        new_commands = []
        for cmd in cmds:
            if 'cmd' in cmd:
                # Simple command
                new_commands.append({'run': cmd['cmd']})
            elif 'run' in cmd:
                # Already in new format
                new_commands.append({'run': cmd['run']})
            elif 'script' in cmd:
                # Already in new format
                new_commands.append({'script': cmd['script']})
            else:
                self.warn(f"Unknown command format: {cmd}", "actions.commands")

        return new_commands

    def _convert_copyfiles(self, copyfiles: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
        """Convert copyfiles to files format"""
        self.log(f"Converting {len(copyfiles)} files...")

        new_files = []
        for file in copyfiles:
            src = file.get('src', '')
            dest = file.get('dest', '')

            if not dest:
                self.warn("File missing destination path - skipping", "layer.files")
                continue

            new_file = {'path': dest}

            # Source file
            if src:
                new_file['src'] = src

            new_files.append(new_file)

        return new_files

    def _convert_ansible_layer(self, old_config: Dict[str, Any]) -> Dict[str, Any]:
        """Convert ansible layer type to commands-based approach"""
        options = old_config.get('options', {})

        self.warn(
            "Ansible layer type is not supported in image-thrillhouse. "
            "You will need to manually convert ansible tasks to commands/scripts.",
            "layer_type"
        )

        layer = {}

        # Create a placeholder manager
        layer['manager'] = {'name': 'dnf'}  # Default, user should adjust

        # Create actions with a comment/warning
        layer['actions'] = {
            'commands': [
                {
                    'script': '#!/bin/bash\n'
                              '# TODO: Convert ansible playbook to shell commands\n'
                              f"# Original playbook: {options.get('playbooks', 'N/A')}\n"
                              f"# Original inventory: {options.get('inventory', 'N/A')}\n"
                              f"# Original groups: {options.get('groups', 'N/A')}\n"
                              '# Add your shell commands here\n'
                              'echo "Ansible conversion required"\n'
                }
            ]
        }

        return layer

    def _convert_publish(self, options: Dict[str, Any]) -> List[Dict[str, Any]]:
        """Convert publish options to publish section"""
        self.log("Converting publish section...")

        publish = []

        # Local publish
        if options.get('publish_local', False):
            publish.append({'type': 'local'})

        # Registry publish
        if 'publish_registry' in options:
            registry_publish = {
                'type': 'registry',
                'url': options['publish_registry']
            }

            # Check for TLS verify option
            if 'registry_opts_push' in options:
                if '--tls-verify=false' in options['registry_opts_push']:
                    registry_publish['tls-verify'] = False

            publish.append(registry_publish)

        # S3 publish
        if 'publish_s3' in options:
            s3_publish = {
                'type': 's3',
                'url': options['publish_s3']
            }

            if 'publish_dest' in options:
                # Try to parse bucket/prefix from publish_dest
                dest = options['publish_dest']
                if '/' in dest:
                    parts = dest.split('/', 1)
                    s3_publish['bucket'] = parts[0]
                    if len(parts) > 1:
                        s3_publish['prefix'] = parts[1]

            publish.append(s3_publish)

        # Squashfs publish (if explicitly mentioned)
        if options.get('publish_squashfs', False):
            squashfs_path = options.get('squashfs_path', '/output/image.squashfs')
            publish.append({
                'type': 'squashfs',
                'path': squashfs_path
            })

        return publish


def main():
    parser = argparse.ArgumentParser(
        description='Convert image-builder config to image-thrillhouse format',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  %(prog)s old-config.yaml
  %(prog)s old-config.yaml -o new-config.yaml
  %(prog)s old-config.yaml --dry-run
  %(prog)s old-config.yaml -o new-config.yaml --verbose
        """
    )

    parser.add_argument(
        'input',
        type=Path,
        help='Input YAML config file (old format)'
    )

    parser.add_argument(
        '-o', '--output',
        type=Path,
        help='Output YAML config file (new format). If not specified, prints to stdout'
    )

    parser.add_argument(
        '--dry-run',
        action='store_true',
        help='Show what would be converted without writing output'
    )

    parser.add_argument(
        '-v', '--verbose',
        action='store_true',
        help='Enable verbose output'
    )

    args = parser.parse_args()

    # Check input file exists
    if not args.input.exists():
        print(f"ERROR: Input file not found: {args.input}", file=sys.stderr)
        sys.exit(1)

    # Load input config
    try:
        with open(args.input, 'r') as f:
            old_config = yaml.safe_load(f)
    except Exception as e:
        print(f"ERROR: Failed to load input YAML: {e}", file=sys.stderr)
        sys.exit(1)

    # Convert
    converter = ConfigConverter(verbose=args.verbose)
    try:
        new_config = converter.convert(old_config)
    except Exception as e:
        print(f"ERROR: Conversion failed: {e}", file=sys.stderr)
        sys.exit(1)

    # Output
    if args.dry_run:
        print("\n--- DRY RUN: Converted configuration ---\n", file=sys.stderr)
        print(yaml.dump(new_config, default_flow_style=False, sort_keys=False))
        print("\n--- END DRY RUN ---\n", file=sys.stderr)
    elif args.output:
        try:
            with open(args.output, 'w') as f:
                yaml.dump(new_config, f, default_flow_style=False, sort_keys=False)
            print(f"Successfully converted {args.input} -> {args.output}", file=sys.stderr)
        except Exception as e:
            print(f"ERROR: Failed to write output: {e}", file=sys.stderr)
            sys.exit(1)
    else:
        # Print to stdout
        print(yaml.dump(new_config, default_flow_style=False, sort_keys=False))

    # Print summary
    if converter.warnings:
        print(f"\n=== Conversion Summary ===", file=sys.stderr)
        print(f"Total warnings: {len(converter.warnings)}", file=sys.stderr)
        print("\nPlease review the warnings above and verify the converted configuration.", file=sys.stderr)
        print("Some manual adjustments may be required, especially for:", file=sys.stderr)
        print("  - Ansible layer types", file=sys.stderr)
        print("  - Complex repository configurations", file=sys.stderr)
        print("  - APT/Debian repositories", file=sys.stderr)
    else:
        print("\nConversion completed successfully with no warnings!", file=sys.stderr)

    sys.exit(0 if not converter.warnings else 0)  # Exit 0 even with warnings


if __name__ == '__main__':
    main()
