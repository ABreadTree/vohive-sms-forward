package netif

import "runtime"

// MeteredSupported is a capability, not a privilege check: Linux has no
// equivalent of the macOS expensive/constrained interface flags.
func MeteredSupported() bool { return runtime.GOOS == "darwin" }
