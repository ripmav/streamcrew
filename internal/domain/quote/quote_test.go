// SPDX-License-Identifier: MIT

package quote_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/domain/quote"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	assert.NoError(t, quote.Quote{Text: "It works on my machine."}.Validate())
	assert.NoError(t, quote.Quote{Number: 7, Text: "x"}.Validate())
	assert.ErrorIs(t, quote.Quote{Text: " \t"}.Validate(), quote.ErrInvalid)
	assert.ErrorIs(t, quote.Quote{Number: -1, Text: "x"}.Validate(), quote.ErrInvalid)
}
