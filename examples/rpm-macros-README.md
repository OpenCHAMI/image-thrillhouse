# Custom RPM Macros Support

## Overview

The DNF and Zypper backends now support custom RPM macros via the `options` configuration. This allows you to override default macros or add new ones that will be written to `/etc/rpm/macros.image-build` during the bootstrap phase of scratch builds.

## Configuration

Custom RPM macros are specified using the `macro.` prefix in the manager options:

```yaml
layer:
  manager:
    name: dnf  # or zypper
    options:
      # Standard options
      releasever: "8"
      install-weak-deps: "false"
      
      # Custom RPM macros using macro.* prefix
      macro._dbpath: /var/lib/rpm
      macro._dbpath_trans: /var/lib/rpm
      
      # Override default macros
      macro._netsharedpath: /sys:/proc
```

## How It Works

1. **Default Macros**: The following RPM macros are automatically included:
   - `%_netsharedpath /sys:/proc:/dev` - Prevents RPM from installing into shared kernel pseudo-fs mounts
   - `%_install_langs C:en:en_US:en_US.UTF-8` - Limits installed locales (smaller images)
   - `%__brp_mangle_shebangs %{nil}` - Disables shebang rewriting
   - `%_missing_build_ids_terminate_build 0` - Don't fail on missing build-ids
   - `%_file_context_file %{nil}` - Suppress SELinux file-context lookups
   - `%__brp_ldconfig %{nil}` - Disables ldconfig execution

2. **Custom Macros**: Options with the `macro.` prefix are extracted and added to the macro file
   - Format: `macro.<macro_name>: <macro_value>`
   - The `macro.` prefix is stripped when writing to the file
   - Example: `macro._dbpath: /var/lib/rpm` → `%_dbpath /var/lib/rpm`

3. **Override Behavior**: Custom macros can override defaults
   - If you specify `macro._netsharedpath: /sys:/proc`, it replaces the default `/sys:/proc:/dev`

## Use Cases

### Database Path Configuration

Control where RPM stores its database:

```yaml
options:
  macro._dbpath: /var/lib/rpm
  macro._dbpath_trans: /var/lib/rpm
```

### Network Path Restrictions

Customize which paths RPM should treat as network-shared:

```yaml
options:
  macro._netsharedpath: /sys:/proc  # Exclude /dev from default
```

### Build Policy Customization

Add custom build-root policy macros:

```yaml
options:
  macro.__brp_python_bytecompile: /usr/bin/python3 -m compileall
  macro._enable_debug_packages: 0
```

## Complete Example

See [rpm-macros-example.yaml](./rpm-macros-example.yaml) for a complete working example.

## Backend Support

- **DNF**: ✅ Supported
- **Zypper**: ✅ Supported
- **APT**: ❌ Not applicable (Debian-based systems don't use RPM)
- **mmdebstrap**: ❌ Not applicable (Debian-based systems don't use RPM)

## Implementation Details

### Files Modified

1. `internal/backend/cmdutil/bootstrap.go`
   - `BuildRPMMacros()` - Merges default and custom macros
   - `ExtractMacroOptions()` - Extracts `macro.*` options from config
   - `WriteRPMMacros()` - Updated to accept custom macros

2. `internal/backend/cmdutil/options.go`
   - `ValidateOptionSchema()` - Allows `macro.*` for RPM backends

3. `internal/backend/dnf/dnf.go`
   - `DnfBackend` struct - Added `customMacros` field
   - `New()` - Extracts macro options during initialization
   - `Bootstrap()` - Passes custom macros to `WriteRPMMacros()`

4. `internal/backend/zypper/zypper.go`
   - `ZypperBackend` struct - Added `customMacros` field
   - `New()` - Extracts macro options during initialization
   - `Bootstrap()` - Passes custom macros to `WriteRPMMacros()`

### Validation

- Options with the `macro.` prefix are automatically validated for DNF and Zypper backends
- Any value is accepted for macro options (treated as `OptionAny`)
- Non-RPM backends will reject `macro.*` options

## Testing

Run the tests to verify the implementation:

```bash
go test ./internal/backend/cmdutil/...
```

The test suite includes:
- `TestExtractMacroOptions` - Verifies macro extraction logic
- `TestBuildRPMMacros` - Tests macro file generation and override behavior
- `TestValidateOptionSchema_MacroPrefix` - Validates backend-specific macro support
