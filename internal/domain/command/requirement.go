// SPDX-License-Identifier: Apache-2.0

package command

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"

	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/polydoc"
)

// Requirement is a condition that must hold before a command runs (B40 to
// B47). Each requirement type appears at most once per command.
type Requirement interface {
	polydoc.Document
	// Validate checks the configuration.
	Validate() error
}

// Requirement type IDs.
const (
	TypeRole      = "role"
	TypeCooldown  = "cooldown"
	TypeCurrency  = "currency"
	TypeRank      = "rank"
	TypeInventory = "inventory"
	TypeArguments = "arguments"
	TypeThreshold = "threshold"
	TypeSettings  = "settings"
)

// requirementTypes lists the requirement types of this package.
func requirementTypes() []polydoc.Entry[Requirement] {
	return []polydoc.Entry[Requirement]{
		{Type: TypeRole, Version: 1, Decode: decodeRequirement[RoleRequirement]},
		{Type: TypeCooldown, Version: 2, Decode: decodeRequirement[CooldownRequirement], Migrations: []polydoc.Migration{migrateCooldownV1}},
		{Type: TypeCurrency, Version: 1, Decode: decodeRequirement[CurrencyRequirement]},
		{Type: TypeRank, Version: 1, Decode: decodeRequirement[RankRequirement]},
		{Type: TypeInventory, Version: 1, Decode: decodeRequirement[InventoryRequirement]},
		{Type: TypeArguments, Version: 1, Decode: decodeRequirement[ArgumentsRequirement]},
		{Type: TypeThreshold, Version: 1, Decode: decodeRequirement[ThresholdRequirement]},
		{Type: TypeSettings, Version: 1, Decode: decodeRequirement[SettingsRequirement]},
	}
}

// decodeRequirement decodes without validating: a stored requirement that
// no longer validates, e.g. after a currency was deleted, must not make the
// command unreadable (B63).
func decodeRequirement[R Requirement](data []byte, opts json.Options) (Requirement, error) {
	r, err := polydoc.Strict[R](data, opts)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// RoleRequirement asks for a minimum role (B40). Without it, the minimum is
// role.User.
type RoleRequirement struct {
	Role role.Role `json:"role"`
}

// DocType implements polydoc.Document.
func (RoleRequirement) DocType() string { return TypeRole }

// Validate implements Requirement.
func (r RoleRequirement) Validate() error {
	if !r.Role.Valid() {
		return fmt.Errorf("unknown role %q", r.Role)
	}
	return nil
}

// CooldownScope says whom a cooldown applies to (B41).
type CooldownScope string

// Cooldown scopes.
const (
	// CooldownStandard applies to everyone.
	CooldownStandard CooldownScope = "standard"
	// CooldownGrouped applies to everyone and all commands of the cooldown
	// group (B33).
	CooldownGrouped CooldownScope = "group"
	// CooldownPerUser applies to each user separately.
	CooldownPerUser CooldownScope = "per_user"
	// CooldownPerUserGrouped applies to each user separately, across the
	// cooldown group.
	CooldownPerUserGrouped CooldownScope = "per_user_group"
)

// Grouped reports whether the scope shares its cooldown through a cooldown
// group (B33).
func (s CooldownScope) Grouped() bool {
	return s == CooldownGrouped || s == CooldownPerUserGrouped
}

// PerUser reports whether the scope applies to each user separately
// (requirements.md, B20).
func (s CooldownScope) PerUser() bool {
	return s == CooldownPerUser || s == CooldownPerUserGrouped
}

// CooldownKey names a running cooldown (requirements.md, B20, B22): the
// command or the cooldown group it blocks, and for the scopes per user the
// user. Exactly one of Command and Group is set.
type CooldownKey struct {
	// Command is the command of the scopes standard and per_user; zero for
	// the grouped scopes.
	Command id.ID
	// Group is the cooldown group of the grouped scopes; zero for the
	// others.
	Group id.ID
	// User is the user of the scopes per user; zero for the others.
	User id.ID
}

// CooldownRequirement blocks a command for a duration after it was queued
// (B41). The scopes standard and per_user have their own Duration; the
// grouped scopes name a cooldown group and take its duration (B33).
//
// Version 1 had a duration for every scope and shared the grouped scopes
// through the command group. Its migration drops that duration; such a
// requirement names no cooldown group until the streamer picks one, and
// the command is faulty until then (requirements.md, B7).
type CooldownRequirement struct {
	Scope CooldownScope `json:"scope"`
	// Duration is the duration of the scopes standard and per_user; zero
	// for the grouped scopes.
	Duration polydoc.Duration `json:"duration,omitzero"`
	// Group is the cooldown group of the grouped scopes; zero for the
	// others.
	Group id.ID `json:"group,omitzero"`
}

// DocType implements polydoc.Document.
func (CooldownRequirement) DocType() string { return TypeCooldown }

// Validate implements Requirement.
func (r CooldownRequirement) Validate() error {
	switch r.Scope {
	case CooldownStandard, CooldownPerUser:
		if r.Duration <= 0 {
			return errors.New("the cooldown duration must be positive")
		}
		if !r.Group.IsZero() {
			return fmt.Errorf("scope %q has no cooldown group", r.Scope)
		}
	case CooldownGrouped, CooldownPerUserGrouped:
		if r.Group.IsZero() {
			return fmt.Errorf("scope %q needs a cooldown group", r.Scope)
		}
		if r.Duration != 0 {
			return fmt.Errorf("scope %q takes the duration of its cooldown group", r.Scope)
		}
	default:
		return fmt.Errorf("unknown cooldown scope %q", r.Scope)
	}
	return nil
}

// Key returns the key of the cooldown r of the command commandID for the
// user userID (requirements.md, B20): the command or the cooldown group by
// the scope, and the user for the scopes per user only. The scopes per user
// need a user; r must be valid.
func (r CooldownRequirement) Key(commandID, userID id.ID) (CooldownKey, error) {
	var k CooldownKey
	if r.Scope.Grouped() {
		k.Group = r.Group
	} else {
		k.Command = commandID
	}
	if r.Scope.PerUser() {
		if userID.IsZero() {
			return CooldownKey{}, fmt.Errorf("cooldown scope %q needs a user", r.Scope)
		}
		k.User = userID
	}
	if k.Command.IsZero() && k.Group.IsZero() {
		return CooldownKey{}, fmt.Errorf("cooldown scope %q without a command or cooldown group", r.Scope)
	}
	return k, nil
}

// migrateCooldownV1 drops the duration of the grouped scopes of version 1;
// version 2 takes it from the cooldown group.
func migrateCooldownV1(doc map[string]jsontext.Value) error {
	raw, ok := doc["scope"]
	if !ok {
		return errors.New("cooldown scope missing")
	}
	var scope CooldownScope
	if err := json.Unmarshal(raw, &scope); err != nil {
		return fmt.Errorf("cooldown scope: %w", err)
	}
	if scope.Grouped() {
		delete(doc, "duration")
	}
	return nil
}

// CurrencyMode says how a currency requirement charges (B42).
type CurrencyMode string

// Currency modes.
const (
	// CurrencyRequired charges a fixed amount.
	CurrencyRequired CurrencyMode = "required"
	// CurrencyMinimum charges the amount the user names, at least Amount.
	CurrencyMinimum CurrencyMode = "minimum"
	// CurrencyRange charges the amount the user names, from Amount to
	// Maximum.
	CurrencyRange CurrencyMode = "range"
)

// CurrencyRequirement charges an amount of a currency (B42).
type CurrencyRequirement struct {
	Currency id.ID        `json:"currency"`
	Mode     CurrencyMode `json:"mode"`
	Amount   int64        `json:"amount"`
	// Maximum is the upper bound of the range mode.
	Maximum int64 `json:"maximum,omitzero"`
}

// DocType implements polydoc.Document.
func (CurrencyRequirement) DocType() string { return TypeCurrency }

// Validate implements Requirement.
func (r CurrencyRequirement) Validate() error {
	if r.Currency.IsZero() {
		return errors.New("no currency")
	}
	if r.Amount < 0 {
		return errors.New("negative amount")
	}
	switch r.Mode {
	case CurrencyRequired, CurrencyMinimum:
		if r.Maximum != 0 {
			return fmt.Errorf("mode %q has no maximum", r.Mode)
		}
	case CurrencyRange:
		if r.Maximum < r.Amount {
			return errors.New("the maximum is below the amount")
		}
	default:
		return fmt.Errorf("unknown currency mode %q", r.Mode)
	}
	return nil
}

// RankMatch says how a rank requirement compares (B43).
type RankMatch string

// Rank comparisons.
const (
	RankAtLeast RankMatch = "at_least"
	RankExactly RankMatch = "exactly"
	RankAtMost  RankMatch = "at_most"
)

// RankRequirement asks for a rank of a currency (B43).
type RankRequirement struct {
	Rank  id.ID     `json:"rank"`
	Match RankMatch `json:"match"`
}

// DocType implements polydoc.Document.
func (RankRequirement) DocType() string { return TypeRank }

// Validate implements Requirement.
func (r RankRequirement) Validate() error {
	if r.Rank.IsZero() {
		return errors.New("no rank")
	}
	switch r.Match {
	case RankAtLeast, RankExactly, RankAtMost:
		return nil
	default:
		return fmt.Errorf("unknown rank comparison %q", r.Match)
	}
}

// InventoryRequirement asks for an amount of an inventory item, which is
// taken when the command runs (B44).
type InventoryRequirement struct {
	Item   id.ID `json:"item"`
	Amount int64 `json:"amount"`
}

// DocType implements polydoc.Document.
func (InventoryRequirement) DocType() string { return TypeInventory }

// Validate implements Requirement.
func (r InventoryRequirement) Validate() error {
	if r.Item.IsZero() {
		return errors.New("no item")
	}
	if r.Amount < 1 {
		return errors.New("the amount must be at least 1")
	}
	return nil
}

// ArgumentType is the type of a command argument (B45).
type ArgumentType string

// Argument types (requirements.md, B33).
const (
	// ArgumentText takes any word, the last argument the rest of the text.
	ArgumentText ArgumentType = "text"
	// ArgumentNumber takes a decimal number with a point.
	ArgumentNumber ArgumentType = "number"
	// ArgumentInteger takes a whole number of 64 bits.
	ArgumentInteger ArgumentType = "integer"
	// ArgumentUser takes a user the platform knows, or any name with "@".
	ArgumentUser ArgumentType = "user"
)

// Argument describes one argument of a command (B45).
type Argument struct {
	Name     string       `json:"name"`
	Type     ArgumentType `json:"type"`
	Required bool         `json:"required"`
	// Identifier is the name under which the value is available in
	// templates, without "$"; empty for none.
	Identifier string `json:"identifier,omitempty"`
}

// ArgumentsRequirement describes the arguments of a command in order (B45).
type ArgumentsRequirement struct {
	Arguments []Argument `json:"arguments"`
}

// DocType implements polydoc.Document.
func (ArgumentsRequirement) DocType() string { return TypeArguments }

// Validate implements Requirement.
func (r ArgumentsRequirement) Validate() error {
	if len(r.Arguments) == 0 {
		return errors.New("no arguments")
	}
	names := make(map[string]bool, len(r.Arguments))
	identifiers := make(map[string]bool, len(r.Arguments))
	for _, a := range r.Arguments {
		if a.Name == "" {
			return errors.New("argument without a name")
		}
		if names[a.Name] {
			return fmt.Errorf("argument %q appears more than once", a.Name)
		}
		names[a.Name] = true
		switch a.Type {
		case ArgumentText, ArgumentNumber, ArgumentInteger, ArgumentUser:
		default:
			return fmt.Errorf("argument %q: unknown type %q", a.Name, a.Type)
		}
		if a.Identifier == "" {
			continue
		}
		if !identifierName(a.Identifier) {
			return fmt.Errorf("argument %q: identifier %q: only lowercase letters and digits", a.Name, a.Identifier)
		}
		if identifiers[a.Identifier] {
			return fmt.Errorf("identifier %q appears more than once", a.Identifier)
		}
		identifiers[a.Identifier] = true
	}
	return nil
}

// identifierName reports whether s can be read as an identifier name by the
// template engine, which reads lowercase ASCII letters and digits (plan
// §6.10).
func identifierName(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return s != ""
}

// ThresholdRequirement lets a command run only after enough different users
// triggered it within a time window (B46).
type ThresholdRequirement struct {
	Users  int              `json:"users"`
	Within polydoc.Duration `json:"within"`
	// RunForEachUser runs the command once for each of these users.
	RunForEachUser bool `json:"runForEachUser"`
}

// DocType implements polydoc.Document.
func (ThresholdRequirement) DocType() string { return TypeThreshold }

// Validate implements Requirement.
func (r ThresholdRequirement) Validate() error {
	if r.Users < 1 {
		return errors.New("at least one user")
	}
	if r.Within <= 0 {
		return errors.New("the time window must be positive")
	}
	return nil
}

// SettingsRequirement holds the per-command settings of the requirements
// page (B47).
type SettingsRequirement struct {
	// DeleteTriggerMessage deletes the chat message that triggered the
	// command once its requirements decided, also if they rejected it
	// (spec requirements.md, B61).
	DeleteTriggerMessage bool `json:"deleteTriggerMessage"`
	// ShowInChatMenu offers the command in the context menu of the chat.
	ShowInChatMenu bool `json:"showInChatMenu"`
}

// DocType implements polydoc.Document.
func (SettingsRequirement) DocType() string { return TypeSettings }

// Validate implements Requirement.
func (SettingsRequirement) Validate() error { return nil }

// UnknownRequirement keeps a requirement this version cannot read; it is
// saved unchanged and never met (Code-ADR-0010).
type UnknownRequirement struct{ polydoc.Unknown }

// DocType implements polydoc.Document.
func (u UnknownRequirement) DocType() string { return u.Type }

// RawJSON implements polydoc.Raw.
func (u UnknownRequirement) RawJSON() jsontext.Value { return u.Raw }

// Validate implements Requirement: unknown requirements are kept as they
// are.
func (UnknownRequirement) Validate() error { return nil }
