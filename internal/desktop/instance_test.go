package desktop

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInstanceID(t *testing.T) {
	assert.Equal(t, UniqueID, instanceID(""))
	a, b := instanceID("/tmp/a"), instanceID("/tmp/b")
	assert.NotEqual(t, a, b)
	assert.Regexp(t, `^ru\.spk\.spk-mattermost\.h[0-9a-f]{8}$`, a)
	assert.Equal(t, a, instanceID("/tmp/a"))
}

func TestParseGPUPolicy(t *testing.T) {
	cases := map[string]gpuPolicy{
		"": gpuDefault, "junk": gpuDefault, "always": gpuAlways,
		"OnDemand": gpuOnDemand, " never ": gpuNever, "off": gpuNever,
	}
	for in, want := range cases {
		assert.Equal(t, want, parseGPUPolicy(in), in)
	}
}
