// SPDX-License-Identifier: MIT

package api

import (
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/decimal"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/counter"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
)

// TestAPIErrorMapping checks that the application sentinels map to the
// client error codes, not CodeInternal (Code-Review 2026-10-24, U5).
func TestAPIErrorMapping(t *testing.T) {
	t.Parallel()

	assert.Nil(t, apiError(nil))

	tests := []struct {
		name string
		err  error
		code connect.Code
	}{
		{"not found", store.ErrNotFound, connect.CodeNotFound},
		{"conflict", store.ErrConflict, connect.CodeAlreadyExists},
		{"restore running", ErrRestoreRunning, connect.CodeFailedPrecondition},
		{"login in progress", ErrLoginInProgress, connect.CodeAborted},
		{"counter invalid", counter.ErrInvalid, connect.CodeInvalidArgument},
		{"counter overflow", counter.ErrOverflow, connect.CodeInvalidArgument},
		{"counter reserved", counter.ErrReserved, connect.CodeInvalidArgument},
		{"decimal syntax", fmt.Errorf("value %q: %w", "abc", decimal.ErrSyntax), connect.CodeInvalidArgument},
		{"decimal range", decimal.ErrRange, connect.CodeInvalidArgument},
		{"id invalid", fmt.Errorf("%w: %q", id.ErrInvalid, "nope"), connect.CodeInvalidArgument},
		{"role unknown", role.ErrUnknown, connect.CodeInvalidArgument},
		{"statistic unknown", user.ErrUnknownStat, connect.CodeInvalidArgument},
		{"settings invalid", settings.ErrInvalid, connect.CodeInvalidArgument},
		{"command invalid", command.ErrInvalid, connect.CodeInvalidArgument},
		{"wrapped not found", fmt.Errorf("outer: %w", store.ErrNotFound), connect.CodeNotFound},
		{"internal", errors.New("disk full"), connect.CodeInternal},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := apiError(tc.err)
			assert.Equal(t, tc.code, connect.CodeOf(got))
		})
	}
}
