// SPDX-License-Identifier: MIT

package logging

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsSecretKey(t *testing.T) {
	t.Parallel()
	for _, key := range []string{
		"token", "access_token", "refreshToken", "Authorization", "client_secret",
		"password", "db_passwd", "Cookie", "apiKey", "api_key", "credentials", "private_key",
	} {
		assert.True(t, isSecretKey(key), key)
	}
	for _, key := range []string{"user", "component", "addr", "msg", "command_id", "keyboard"} {
		assert.False(t, isSecretKey(key), key)
	}
}
