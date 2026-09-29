// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExitCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		err        error
		wantCode   int
		wantStderr string
	}{
		{name: "success", err: nil, wantCode: ExitOK},
		{name: "failure", err: errors.New("boom"), wantCode: ExitFailure, wantStderr: "streamcrew: boom\n"},
		{name: "usage", err: fmt.Errorf("check: %w", &usageError{err: errors.New("bad flag")}), wantCode: ExitUsage, wantStderr: "streamcrew: check: bad flag\n"},
		{name: "reported", err: &reportedError{err: errors.New("logged")}, wantCode: ExitFailure},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			assert.Equal(t, tc.wantCode, ExitCode(tc.err, &stderr))
			assert.Equal(t, tc.wantStderr, stderr.String())
		})
	}
}
