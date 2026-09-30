// SPDX-License-Identifier: Apache-2.0

package template

import "context"

// [Interop] The identifier names in this file follow the original (spec
// template.md, purpose and scope; spec events.md, B7) and may be replaced
// after the legal assessment (roadmap Gate O, O.1).

// RunFamily returns the identifiers of the run: $commandname and
// $streamingplatform, the platform as it writes its name, e.g. "Twitch".
// Without a command or platform they have no value (B4).
func RunFamily() Family {
	text := func(field func(s *Scope) string) Resolver {
		return func(_ context.Context, s *Scope) (Value, bool, error) {
			if v := field(s); v != "" {
				return TextValue(v), true, nil
			}
			return Value{}, false, nil
		}
	}
	return Family{
		Name: "run",
		Identifiers: []Identifier{
			{Name: "commandname", Resolve: text(func(s *Scope) string { return s.CommandName })},
			{Name: "streamingplatform", Resolve: text(func(s *Scope) string { return s.Platform.DisplayName() })},
		},
	}
}

// Names of the event values (spec events.md, B7). The event service sets
// them with Scope.SetValue (roadmap 3.6); as values of the run they rank
// before the built-in identifiers, so an event's $message is the message of
// the event (B10).
const (
	EventRaidViewerCount = "raidviewercount"
	EventMessage         = "message"
	EventSubPlan         = "usersubplan"
	EventSubPlanName     = "usersubplanname"
	EventAnonymous       = "isanonymous"
	EventGiftedSubs      = "subsgiftedamount"
)
