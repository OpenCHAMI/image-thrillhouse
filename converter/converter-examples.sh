#!/bin/bash
# SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
#
# SPDX-License-Identifier: MIT
# Example usage of the config converter

echo "=== Image Builder Config Converter - Usage Examples ==="
echo ""

# Example 1: Basic conversion to stdout
echo "1. Convert and print to stdout:"
echo "   ./convert-config.py examples/old-config.yaml"
echo ""

# Example 2: Convert to file
echo "2. Convert and save to file:"
echo "   ./convert-config.py examples/old-config.yaml -o examples/new-config.yaml"
echo ""

# Example 3: Dry run
echo "3. Preview conversion (dry-run):"
echo "   ./convert-config.py examples/old-config.yaml --dry-run"
echo ""

# Example 4: Verbose output
echo "4. Convert with verbose logging:"
echo "   ./convert-config.py examples/old-config.yaml -o output.yaml --verbose"
echo ""

# Example 5: Batch conversion (using a loop)
echo "5. Batch convert multiple files:"
echo "   for f in examples/*.yaml; do"
echo "     ./convert-config.py \"\$f\" -o \"converted/\$(basename \$f)\""
echo "   done"
echo ""

echo "=== For more information, see CONVERTER_README.md ==="
