package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// instanceID is the single-instance lock ID. The default data dir uses
// UniqueID; a custom SPK_MATTERMOST_HOME gets its own ID, so a dev/test
// instance can run next to the user's client instead of forwarding to it
// (mmauth:// deep links still go to the default instance).
func instanceID(customHome string) string {
	if customHome == "" {
		return UniqueID
	}
	if abs, err := filepath.Abs(customHome); err == nil {
		customHome = abs
	}
	sum := sha256.Sum256([]byte(customHome))
	// D-Bus name elements must not start with a digit.
	return UniqueID + ".h" + hex.EncodeToString(sum[:4])
}

// gpuPolicy names the WebKitGTK hardware-acceleration policy.
type gpuPolicy int

const (
	gpuDefault gpuPolicy = iota
	gpuAlways
	gpuOnDemand
	gpuNever
)

// parseGPUPolicy reads SPK_MATTERMOST_GPU (always|ondemand|never); anything
// else keeps the default. An escape hatch for broken GPU drivers.
func parseGPUPolicy(v string) gpuPolicy {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "always":
		return gpuAlways
	case "ondemand", "on-demand":
		return gpuOnDemand
	case "never", "off":
		return gpuNever
	}
	return gpuDefault
}
