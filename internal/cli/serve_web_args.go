package cli

import (
	_ "embed"
	"encoding/json"
)

// Shared with scripts/build_web.mjs so serve-time builds, npm and the drift
// check always build the same workspace bundles, including newly added ones.
//
//go:embed web-build-args.json
var webBuildManifest []byte

func webBuildArgs() []string {
	var args []string
	if err := json.Unmarshal(webBuildManifest, &args); err != nil {
		panic("invalid embedded web build manifest: " + err.Error())
	}
	return args
}
