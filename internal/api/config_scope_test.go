// SPDX-License-Identifier: MIT

package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/apitoken"
)

// TestConfigServiceRequiresAdmin checks that both methods of the
// ConfigService require the admin scope (roadmap 6.5.2).
func TestConfigServiceRequiresAdmin(t *testing.T) {
	t.Parallel()
	assert.Equal(t, apitoken.Admin, requiredScope("/streamcrew.v1alpha1.ConfigService/Get"))
	assert.Equal(t, apitoken.Admin, requiredScope("/streamcrew.v1alpha1.ConfigService/Update"))
}
