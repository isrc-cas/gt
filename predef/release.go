//go:build release
// +build release

package predef

import (
	// used for prof
	_ "net/http/pprof"
)

// Debug enables the logs of read and write operations
const Debug = false
