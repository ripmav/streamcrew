// SPDX-License-Identifier: MIT

package command

import (
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/domain/role"
)

// RequirementDescriptor describes a requirement type for the type catalog
// and commands as code (Code-ADR-0013, point 3).
type RequirementDescriptor struct {
	Type string
	// Version is the current schema version of the type.
	Version int
	// Schema describes the members of the current version without "type"
	// and "schemaVersion", as stored: references are IDs.
	Schema *schema.Schema
}

// RequirementCatalog returns the requirement types in the order of their
// checks (requirements.md, B2), the settings last.
func RequirementCatalog() []RequirementDescriptor {
	list := []RequirementDescriptor{
		{Type: TypeRole, Schema: schema.Object(
			schema.Property{Name: "role", Schema: schema.Choice(roleIDs()...), Required: true},
		)},
		{Type: TypeBits, Schema: schema.Object(
			schema.Property{Name: "amount", Schema: count(1), Required: true},
		)},
		{Type: TypeCooldown, Schema: schema.Pick("scope", nil,
			schema.Alternative{
				Values: []string{string(CooldownStandard), string(CooldownPerUser)},
				Props:  []schema.Property{{Name: "duration", Schema: schema.Duration(), Required: true}},
			},
			schema.Alternative{
				Values: []string{string(CooldownGrouped), string(CooldownPerUserGrouped)},
				Props:  []schema.Property{{Name: "group", Schema: schema.Reference(schema.UICooldownGroup), Required: true}},
			},
		)},
		{Type: TypeArguments, Schema: schema.Object(
			schema.Property{Name: "arguments", Schema: schema.List(schema.Object(
				schema.Property{Name: "name", Schema: schema.NonEmpty(schema.UIText), Required: true},
				schema.Property{Name: "type", Schema: schema.Choice(
					string(ArgumentText), string(ArgumentNumber), string(ArgumentInteger), string(ArgumentUser),
				), Required: true},
				schema.Property{Name: "required", Schema: schema.Switch()},
				schema.Property{Name: "identifier", Schema: schema.ResultName()},
			), 1), Required: true},
		)},
		{Type: TypeRank, Schema: schema.Object(
			schema.Property{Name: "rank", Schema: schema.Reference(schema.UIRank), Required: true},
			schema.Property{Name: "match", Schema: schema.Choice(
				string(RankAtLeast), string(RankExactly), string(RankAtMost),
			), Required: true},
		)},
		{Type: TypeCurrency, Schema: schema.Pick("mode",
			[]schema.Property{{Name: "currency", Schema: schema.Reference(schema.UICurrency), Required: true}},
			schema.Alternative{
				Values: []string{string(CurrencyRequired), string(CurrencyMinimum)},
				Props:  []schema.Property{{Name: "amount", Schema: count(0), Required: true}},
			},
			schema.Alternative{
				Values: []string{string(CurrencyRange)},
				Props: []schema.Property{
					{Name: "amount", Schema: count(0), Required: true},
					{Name: "maximum", Schema: count(0), Required: true},
				},
			},
		)},
		{Type: TypeInventory, Schema: schema.Object(
			schema.Property{Name: "item", Schema: schema.Reference(schema.UIItem), Required: true},
			schema.Property{Name: "amount", Schema: count(1), Required: true},
		)},
		{Type: TypeThreshold, Schema: schema.Object(
			schema.Property{Name: "users", Schema: count(1), Required: true},
			schema.Property{Name: "within", Schema: schema.Duration(), Required: true},
			schema.Property{Name: "runForEachUser", Schema: schema.Switch()},
		)},
		{Type: TypeSettings, Schema: schema.Object(
			schema.Property{Name: "deleteTriggerMessage", Schema: schema.Switch()},
			schema.Property{Name: "showInChatMenu", Schema: schema.Switch()},
		)},
	}
	versions := make(map[string]int, len(list))
	for _, e := range requirementTypes() {
		versions[e.Type] = e.Version
	}
	for i, d := range list {
		list[i].Version = versions[d.Type]
	}
	return list
}

// count returns the field of a whole number of at least minimum.
func count(minimum float64) *schema.Schema {
	return &schema.Schema{Type: "integer", Minimum: new(minimum)}
}

// roleIDs returns the IDs of the roles in ascending rank.
func roleIDs() []string {
	all := role.All()
	ids := make([]string, len(all))
	for i, r := range all {
		ids[i] = string(r)
	}
	return ids
}
