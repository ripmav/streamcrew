// SPDX-License-Identifier: Apache-2.0

package capability_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/capability"
)

func TestValid(t *testing.T) {
	t.Parallel()
	for _, c := range capability.All() {
		assert.True(t, c.Valid(), c)
	}
	for _, c := range []capability.Capability{"", "host", "HOST:FS", "integration:obs"} {
		assert.False(t, c.Valid(), c)
	}
}

func TestSet(t *testing.T) {
	t.Parallel()
	s, err := capability.NewSet(capability.NetOutbound, capability.HostFS, capability.HostFS)
	require.NoError(t, err)
	assert.True(t, s.Has(capability.HostFS))
	assert.False(t, s.Has(capability.HostProcess))
	assert.Equal(t, []capability.Capability{capability.HostFS, capability.NetOutbound}, s.List(), "in the order of All")

	need := []capability.Capability{capability.HostProcess, capability.NetOutbound, capability.Script, capability.HostProcess}
	assert.Equal(t, []capability.Capability{capability.HostProcess, capability.Script}, s.Missing(need))
	assert.Equal(t, []capability.Capability{}, s.Missing([]capability.Capability{capability.HostFS}), "empty, not nil")

	_, err = capability.NewSet("host:root")
	require.ErrorIs(t, err, capability.ErrUnknown)
}

func TestZeroSetIsEmpty(t *testing.T) {
	t.Parallel()
	var s capability.Set
	assert.False(t, s.Has(capability.HostFS))
	assert.Equal(t, []capability.Capability{}, s.List())
	assert.Equal(t, []capability.Capability{capability.Script}, s.Missing([]capability.Capability{capability.Script}))
}
