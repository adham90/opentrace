package managed

import "os"

// Enabled returns true when running under the managed cloud platform.
// Set OPENTRACE_MANAGED=true to enable managed mode.
func Enabled() bool {
	return os.Getenv("OPENTRACE_MANAGED") == "true"
}
