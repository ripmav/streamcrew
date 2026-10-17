// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/ripmav/streamcrew/internal/api"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/commandfile"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/engine"
)

// apiCommands is the api.Commands port over the command service, the engine
// and the commandfile round-trip (roadmap 6.2.1).
type apiCommands struct {
	a *App
}

var _ api.Commands = apiCommands{}

// groupNames maps the IDs of the command groups to their names.
func (c apiCommands) groupNames(ctx context.Context) (map[string]string, error) {
	groups, err := c.a.commands.Groups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the groups: %w", err)
	}
	out := make(map[string]string, len(groups))
	for _, g := range groups {
		out[g.ID.String()] = g.Name
	}
	return out, nil
}

// summary converts a command to its API view.
func (c apiCommands) summary(cmd command.Command, groups map[string]string) api.CommandSummary {
	return api.CommandSummary{
		ID:        cmd.ID.String(),
		Name:      cmd.Name,
		Kind:      string(cmd.Kind),
		Enabled:   cmd.Enabled,
		Triggers:  cmd.Triggers,
		EventType: string(cmd.Event),
		Channel:   cmd.Channel,
		Group:     groups[cmd.GroupID.String()],
		UpdatedAt: cmd.UpdatedAt,
	}
}

// source is the export source of the live profile.
func (c apiCommands) source(ctx context.Context) (commandfile.Source, error) {
	var src commandfile.Source
	var err error
	if src.Commands, err = c.a.store.Commands(ctx); err != nil {
		return src, err
	}
	if src.Groups, err = c.a.store.Groups(ctx); err != nil {
		return src, err
	}
	if src.CooldownGroups, err = c.a.store.CooldownGroups(ctx); err != nil {
		return src, err
	}
	return src, nil
}

// types are the action types and the codec for the commandfile round-trip.
func (c apiCommands) types(_ context.Context) (commandfile.Types, error) {
	actions, err := ActionCatalog(capability.Set{})
	if err != nil {
		return commandfile.Types{}, err
	}
	codec, err := command.NewCodec(actions.Entries()...)
	if err != nil {
		return commandfile.Types{}, err
	}
	return commandfile.Types{Actions: actions.Descriptors(), Codec: codec}, nil
}

// readDocument reads a command document in its format (yaml or json).
func (c apiCommands) readDocument(document, format string) ([]commandfile.Document, []commandfile.Problem) {
	name := "<api>.yaml"
	if format == "json" {
		name = "<api>.json"
	}
	return commandfile.Read(name, []byte(document))
}

// applyDocs converts and applies documents to the live profile; a document
// with errors is not applied (B62).
func (c apiCommands) applyDocs(ctx context.Context, docs []commandfile.Document) (commandfile.Plan, []commandfile.Problem, error) {
	existing, err := existingObjects(ctx, c.a.store)
	if err != nil {
		return commandfile.Plan{}, nil, err
	}
	types, err := c.types(ctx)
	if err != nil {
		return commandfile.Plan{}, nil, err
	}
	plan, problems := commandfile.Convert(docs, existing, types)
	plan, conflicts := commandfile.Conflicts(plan, existing)
	problems = append(problems, conflicts...)
	if commandfile.HasErrors(problems) {
		return plan, problems, nil
	}
	saved, err := commandfile.Apply(ctx, plan, c.a.commands, types)
	return plan, append(problems, saved...), err
}

// exportDocument writes the commands named (or all, without names) to a
// document in the format, with the groups they refer to.
func (c apiCommands) exportDocument(ctx context.Context, names []string, format string) (string, error) {
	src, err := c.source(ctx)
	if err != nil {
		return "", err
	}
	types, err := c.types(ctx)
	if err != nil {
		return "", err
	}
	exported, err := commandfile.Export(src, names, types)
	if err != nil {
		return "", fmt.Errorf("export: %w", err)
	}
	if format == "json" {
		b, err := commandfile.EncodeJSON(exported, false)
		return string(b), err
	}
	b, err := commandfile.EncodeYAML(exported)
	return string(b), err
}

// savedCommand finds a command by its name after an import.
func (c apiCommands) savedCommand(ctx context.Context, name string) (command.Command, bool, error) {
	return c.a.commands.CommandByName(ctx, name)
}

func (c apiCommands) List(ctx context.Context, kind string, enabled *bool, prefix string) ([]api.CommandSummary, error) {
	cmds, err := c.a.commands.Commands(ctx)
	if err != nil {
		return nil, fmt.Errorf("list commands: %w", err)
	}
	groups, err := c.groupNames(ctx)
	if err != nil {
		return nil, err
	}
	pfx := strings.ToLower(prefix)
	out := make([]api.CommandSummary, 0, len(cmds))
	for _, cmd := range cmds {
		if kind != "" && string(cmd.Kind) != kind {
			continue
		}
		if enabled != nil && cmd.Enabled != *enabled {
			continue
		}
		if pfx != "" && !strings.HasPrefix(strings.ToLower(cmd.Name), pfx) {
			continue
		}
		out = append(out, c.summary(cmd, groups))
	}
	return out, nil
}

func (c apiCommands) Get(ctx context.Context, idStr string) (api.CommandSummary, string, string, error) {
	cmdID, err := id.Parse(idStr)
	if err != nil {
		return api.CommandSummary{}, "", "", err
	}
	cmd, err := c.a.commands.Command(ctx, cmdID)
	if err != nil {
		return api.CommandSummary{}, "", "", err
	}
	groups, err := c.groupNames(ctx)
	if err != nil {
		return api.CommandSummary{}, "", "", err
	}
	doc, err := c.exportDocument(ctx, []string{cmd.Name}, "yaml")
	if err != nil {
		return api.CommandSummary{}, "", "", err
	}
	return c.summary(cmd, groups), doc, "yaml", nil
}

// oneCommand applies a single-command document and returns the saved command.
func (c apiCommands) oneCommand(ctx context.Context, document, format string) (command.Command, []string, error) {
	docs, problems := c.readDocument(document, format)
	if commandfile.HasErrors(problems) {
		return command.Command{}, nil, fmt.Errorf("the document has errors: %s", firstProblem(problems))
	}
	if len(docs) != 1 {
		return command.Command{}, nil, fmt.Errorf("the document must name one command, not %d", len(docs))
	}
	_, problems, err := c.applyDocs(ctx, docs)
	if err != nil {
		return command.Command{}, nil, err
	}
	if commandfile.HasErrors(problems) {
		return command.Command{}, nil, fmt.Errorf("the document has errors: %s", firstProblem(problems))
	}
	name, _ := docs[0].Name()
	cmd, ok, err := c.savedCommand(ctx, name)
	if err != nil {
		return command.Command{}, nil, err
	}
	if !ok {
		return command.Command{}, nil, fmt.Errorf("the command %q was not saved", name)
	}
	return cmd, warningsFrom(problems), nil
}

func (c apiCommands) Create(ctx context.Context, document, format string) (api.CommandSummary, []string, error) {
	cmd, warnings, err := c.oneCommand(ctx, document, format)
	if err != nil {
		return api.CommandSummary{}, nil, err
	}
	groups, err := c.groupNames(ctx)
	if err != nil {
		return api.CommandSummary{}, nil, err
	}
	return c.summary(cmd, groups), warnings, nil
}

func (c apiCommands) Update(ctx context.Context, idStr, document, format string) (api.CommandSummary, []string, error) {
	cmdID, err := id.Parse(idStr)
	if err != nil {
		return api.CommandSummary{}, nil, err
	}
	if _, err := c.a.commands.Command(ctx, cmdID); err != nil {
		return api.CommandSummary{}, nil, err
	}
	cmd, warnings, err := c.oneCommand(ctx, document, format)
	if err != nil {
		return api.CommandSummary{}, nil, err
	}
	groups, err := c.groupNames(ctx)
	if err != nil {
		return api.CommandSummary{}, nil, err
	}
	return c.summary(cmd, groups), warnings, nil
}

func (c apiCommands) Delete(ctx context.Context, idStr string) error {
	cmdID, err := id.Parse(idStr)
	if err != nil {
		return err
	}
	if err := c.a.commands.Delete(ctx, cmdID); err != nil {
		return fmt.Errorf("delete command: %w", err)
	}
	return nil
}

func (c apiCommands) Run(ctx context.Context, idStr string, args []string, channel string) (string, string, string, error) {
	cmdID, err := id.Parse(idStr)
	if err != nil {
		return "", "", "", err
	}
	cmd, err := c.a.commands.Command(ctx, cmdID)
	if err != nil {
		return "", "", "", err
	}
	params := engine.Params{Channel: channel}
	if len(args) > 0 {
		params.Args = args
		params.ArgsText = strings.Join(args, " ")
	}
	instID, err := c.a.engine.Start(ctx, cmd, params)
	if err != nil {
		//nolint:nilerr // the start error is conveyed as the result and reason, not as an error
		return "", "rejected", err.Error(), nil
	}
	return instID.String(), "started", "", nil
}

func (c apiCommands) ListInstances(_ context.Context, commandID string, states []string, limit uint32) (bool, []api.CommandInstance, error) {
	paused, err := c.a.engine.Paused(engine.PauseAll)
	if err != nil {
		return false, nil, err
	}
	history := c.a.engine.History() // oldest first
	out := make([]api.CommandInstance, 0, len(history))
	for _, ins := range history {
		if commandID != "" && ins.CommandID.String() != commandID {
			continue
		}
		if len(states) > 0 && !slices.Contains(states, string(ins.State)) {
			continue
		}
		out = append(out, c.instanceView(ins))
	}
	slices.Reverse(out) // newest first
	if limit > 0 && len(out) > int(limit) {
		out = out[:int(limit)]
	}
	return paused, out, nil
}

func (c apiCommands) instanceView(ins engine.Instance) api.CommandInstance {
	errText := ""
	if len(ins.Errors) > 0 {
		errText = ins.Errors[0].Message
	}
	return api.CommandInstance{
		InstanceID:  ins.ID.String(),
		CommandID:   ins.CommandID.String(),
		CommandName: ins.CommandName,
		State:       string(ins.State),
		Source:      string(ins.Source),
		StartedAt:   ins.StartedAt,
		EndedAt:     ins.EndedAt,
		Error:       errText,
	}
}

func (c apiCommands) PauseQueue(ctx context.Context, pause bool) (bool, error) {
	if pause {
		if err := c.a.engine.Pause(ctx, engine.PauseAll); err != nil {
			return false, err
		}
	} else {
		if err := c.a.engine.Resume(ctx, engine.PauseAll); err != nil {
			return false, err
		}
	}
	return c.a.engine.Paused(engine.PauseAll)
}

func (c apiCommands) CancelInstance(ctx context.Context, instanceID string) error {
	insID, err := id.Parse(instanceID)
	if err != nil {
		return err
	}
	if err := c.a.engine.Cancel(ctx, insID); err != nil {
		return fmt.Errorf("cancel instance: %w", err)
	}
	return nil
}

func (c apiCommands) RetryInstance(ctx context.Context, instanceID string) (string, error) {
	insID, err := id.Parse(instanceID)
	if err != nil {
		return "", err
	}
	replayed := c.a.engine.Replay(ctx, insID)
	if len(replayed) == 0 {
		return "", fmt.Errorf("the instance %s is not in the history", instanceID)
	}
	if replayed[0].Err != nil {
		return "", replayed[0].Err
	}
	return replayed[0].Instance.String(), nil
}

func (c apiCommands) TypeCatalog(_ context.Context) ([]api.ActionTypeInfo, []api.ActionTypeInfo, error) {
	actions, err := ActionCatalog(capability.Set{})
	if err != nil {
		return nil, nil, err
	}
	outActions := make([]api.ActionTypeInfo, 0, len(actions.Descriptors()))
	for _, d := range actions.Descriptors() {
		if d.Schema == nil {
			continue
		}
		b, err := d.Schema.JSON()
		if err != nil {
			continue
		}
		outActions = append(outActions, api.ActionTypeInfo{
			Type:     d.Type,
			Version:  uint32(d.Version), //nolint:gosec // a schema version fits in uint32
			Category: string(d.Category),
			Schema:   string(b),
		})
	}
	reqs := command.RequirementCatalog()
	outReqs := make([]api.ActionTypeInfo, 0, len(reqs))
	for _, r := range reqs {
		if r.Schema == nil {
			continue
		}
		b, err := r.Schema.JSON()
		if err != nil {
			continue
		}
		outReqs = append(outReqs, api.ActionTypeInfo{
			Type:          r.Type,
			Version:       uint32(r.Version), //nolint:gosec // a schema version fits in uint32
			IsRequirement: true,
			Schema:        string(b),
		})
	}
	return outActions, outReqs, nil
}

func (c apiCommands) Import(ctx context.Context, document, format string) ([]api.ImportResult, error) {
	docs, problems := c.readDocument(document, format)
	if commandfile.HasErrors(problems) {
		return nil, fmt.Errorf("the document has errors: %s", firstProblem(problems))
	}
	plan, problems, err := c.applyDocs(ctx, docs)
	if err != nil {
		return nil, err
	}
	if commandfile.HasErrors(problems) {
		return nil, fmt.Errorf("the document has errors: %s", firstProblem(problems))
	}
	results := make([]api.ImportResult, 0, len(plan.Commands))
	for _, p := range plan.Commands {
		name, _ := p.Doc.Name()
		res := api.ImportResult{Name: name, Created: !p.Replaces}
		if cmd, ok, err := c.savedCommand(ctx, name); err == nil && ok {
			res.ID = cmd.ID.String()
		} else {
			res.Error = "the command was not saved"
		}
		results = append(results, res)
	}
	return results, nil
}

func (c apiCommands) Export(ctx context.Context, ids []string, format string) (string, string, error) {
	if format == "" {
		format = "yaml"
	}
	if len(ids) == 0 {
		doc, err := c.exportDocument(ctx, nil, format)
		return doc, format, err
	}
	names := make([]string, 0, len(ids))
	for _, idStr := range ids {
		cmdID, err := id.Parse(idStr)
		if err != nil {
			return "", "", err
		}
		cmd, err := c.a.commands.Command(ctx, cmdID)
		if err != nil {
			return "", "", err
		}
		names = append(names, cmd.Name)
	}
	doc, err := c.exportDocument(ctx, names, format)
	return doc, format, err
}

// warningsFrom is the message of every warning problem.
func warningsFrom(problems []commandfile.Problem) []string {
	var out []string
	for _, p := range problems {
		if p.Severity == commandfile.SeverityWarning {
			out = append(out, p.Message)
		}
	}
	return out
}

// firstProblem is the message of the first error problem.
func firstProblem(problems []commandfile.Problem) string {
	for _, p := range problems {
		if p.Severity == commandfile.SeverityError {
			return p.Message
		}
	}
	return "an unknown error"
}
