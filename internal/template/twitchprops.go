// SPDX-License-Identifier: MIT

package template

// [Interop] The identifier names in this file are the values of the
// twitch events (spec twitch-events.md, B19); the new twitch names are
// proposals of the core and may be replaced after the legal assessment
// (roadmap Gate O, O.1).

// Names of the values of the twitch events besides the general ones of
// B7 (spec twitch-events.md, table "Identifier je Ereignis"). The event
// service sets them with Scope.SetValue.
const (
	EventCheerBits         = "cheerbits"
	EventModerationMessage = "moderationmessage"
	EventGoalCurrentAmount = "goalcurrentamount"
	EventGoalTargetAmount  = "goaltargetamount"
	EventGoalCurrency      = "goalcurrency"
	EventHypeTrainLevel    = "hypetrainlevel"
	EventHypeTrainProgress = "hypetrainprogress"
	EventHypeTrainGoal     = "hypetraingoal"
	EventHypeTrainReward   = "hypetrainrewardlevel"
	EventHypeTrainOutcome  = "hypetrainoutcome"
	EventAdBreakDuration   = "adbreakduration"
	EventAdBreakMessage    = "adbreakmessage"
	EventShoutoutViewers   = "shoutoutviewers"
	EventDonationCurrent   = "donationcurrentamount"
	EventDonationTarget    = "donationtargetamount"
	EventDonationCurrency  = "donationcurrency"
	EventChannelPoints     = "channelpointsamount"
	EventChannelPointsID   = "channelpointsreward"
	EventCustomPowerUp     = "custompowerup"
)
