//go:build !release
// +build !release

package predef

import (
	"log"
	"net/http"
	// used for prof
	_ "net/http/pprof"
	"os"
	"strings"
)

// Debug enables the logs of read and write operations
var Debug = true

func init() {
	env, ok := os.LookupEnv("DEBUG_REQ")
	if ok {
		if strings.ToLower(env) == "true" {
			Debug = true
		}
	}
	prof, ok := os.LookupEnv("DEBUG_PROF")
	if ok {
		go func() {
			log.Println(http.ListenAndServe(prof, nil))
		}()
	}
}
