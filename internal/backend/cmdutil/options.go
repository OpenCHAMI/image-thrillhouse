package cmdutil

import "fmt"

// OptionKind classifies an option's allowed values. It exists so
// ValidateOptionSchema can apply the right rule per option key without each
// backend re-implementing the same map walks.
type OptionKind int

const (
	// OptionBool restricts the value to "" (treated as default), "true",
	// or "false". This is the common case for backend toggles.
	OptionBool OptionKind = iota
	// OptionString accepts any non-empty value. Use for free-form options
	// like releasever or mirror URLs.
	OptionString
	// OptionAny accepts any value, including empty. Use sparingly; prefer
	// OptionString or OptionBool when the option has a meaningful constraint.
	OptionAny
)

// ValidateOptionSchema checks a backend's options map against a schema of
// allowed keys and their value kinds, replacing the validateOptions
// boilerplate that was previously duplicated across the dnf, zypper, and
// apt backends. backendName is used in error messages so the caller doesn't
// have to wrap.
//
// Unknown keys are an error. For known keys:
//   - OptionBool: value must be "", "true", or "false"
//   - OptionString: value must be non-empty
//   - OptionAny: anything goes
//
// Returns nil on success.
func ValidateOptionSchema(backendName string, options map[string]string, schema map[string]OptionKind) error {
	for key, value := range options {
		kind, ok := schema[key]
		if !ok {
			return fmt.Errorf("unknown option %q for %s backend", key, backendName)
		}
		switch kind {
		case OptionBool:
			if value != "" && value != "true" && value != "false" {
				return fmt.Errorf("option %q must be 'true' or 'false', got %q", key, value)
			}
		case OptionString:
			if value == "" {
				return fmt.Errorf("option %q cannot be empty", key)
			}
		case OptionAny:
			// no constraint
		}
	}
	return nil
}
