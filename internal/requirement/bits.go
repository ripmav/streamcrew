// SPDX-License-Identifier: MIT

package requirement

import (
	"github.com/ripmav/streamcrew/internal/decimal"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/i18n"
	"github.com/ripmav/streamcrew/internal/template"
)

// checkBits checks the bits requirement (roadmap 4.4): the run the command
// is triggered by must carry at least the required bits, e.g. the cheer of
// a twitch.bits.cheer event command. A run without bits never meets it.
func checkBits(cmd command.Command, p engine.Params) (engine.Rejection, bool) {
	r, ok := find[command.BitsRequirement](cmd)
	if !ok {
		return engine.Rejection{}, false
	}
	v, ok := p.Values[template.EventCheerBits]
	if !ok || !v.IsNumber || v.Number.Cmp(decimal.New(r.Amount)) < 0 {
		return engine.Rejection{
			Requirement: command.TypeBits,
			Reason:      i18n.Message{Key: i18n.KeyRequirementBits, Args: map[string]i18n.Value{"amount": i18n.Int(r.Amount)}},
			Tell:        told(p),
		}, true
	}
	return engine.Rejection{}, false
}
