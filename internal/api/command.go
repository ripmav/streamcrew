// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
)

// CommandSummary is the typed view of a command for lists and details.
type CommandSummary struct {
	// ID is the command ID.
	ID string
	// Name is the unique command name.
	Name string
	// Kind is "chat", "event", "timer" or "action_group".
	Kind string
	// Enabled is the "active" switch.
	Enabled bool
	// Triggers are the chat triggers; empty for non-chat commands.
	Triggers []string
	// EventType is the event type; empty for non-event commands.
	EventType string
	// Channel is the channel scope; empty means every operated channel.
	Channel string
	// Group is the command group name; empty if none.
	Group string
	// UpdatedAt is when the command was last changed.
	UpdatedAt time.Time
}

// CommandInstance is the state of one command instance.
type CommandInstance struct {
	InstanceID  string
	CommandID   string
	CommandName string
	// State is "queued", "running", "completed", "failed" or "canceled".
	State string
	// Source is "chat", "event", "timer", "call" or "manual".
	Source  string
	Channel string
	// StartedAt is when the instance started; zero if still queued.
	StartedAt time.Time
	// EndedAt is when the instance ended; zero while running.
	EndedAt time.Time
	// Error is the error message; empty if none.
	Error string
}

// ActionTypeInfo is one action or requirement type of the catalog.
type ActionTypeInfo struct {
	// Type is the type ID, e.g. "chat" or "role".
	Type string
	// Version is the schema version.
	Version uint32
	// Category is the type category.
	Category string
	// Kinds are the kinds of the type; empty if the type has none.
	Kinds []string
	// IsRequirement is true for requirement types, false for action types.
	IsRequirement bool
	// Schema is the JSON schema of the type, for generic editors.
	Schema string
}

// ImportResult is the result of importing one command.
type ImportResult struct {
	// Name is the command name.
	Name string
	// ID is the command ID; empty if the command was not created.
	ID string
	// Created is true if the command was created, false if updated.
	Created bool
	// Error is the import error; empty if the command was imported.
	Error string
}

// Commands manages the commands of a profile (roadmap 6.2.1, API).
type Commands interface {
	// List lists the commands, filtered by kind (empty: all), the active
	// switch (nil: all) and a case-insensitive name prefix (empty: all).
	List(ctx context.Context, kind string, enabled *bool, prefix string) ([]CommandSummary, error)
	// Get returns one command as its document (and format) and summary.
	Get(ctx context.Context, id string) (summary CommandSummary, document, format string, err error)
	// Create creates a command from its document and returns it with warnings.
	Create(ctx context.Context, document, format string) (CommandSummary, []string, error)
	// Update replaces a command with its new document and returns it with warnings.
	Update(ctx context.Context, id, document, format string) (CommandSummary, []string, error)
	// Delete removes a command.
	Delete(ctx context.Context, id string) error
	// SetEnabled switches the active switch of a command.
	SetEnabled(ctx context.Context, id string, enabled bool) (CommandSummary, error)
	// Run starts a command manually and reports the result.
	Run(ctx context.Context, id string, args []string, channel string) (instanceID, result, reason string, err error)
	// ListInstances lists the instances (newest first) and the queue pause.
	ListInstances(ctx context.Context, commandID string, states []string, limit uint32) (paused bool, instances []CommandInstance, err error)
	// PauseQueue pauses the queue when pause, resumes it otherwise.
	PauseQueue(ctx context.Context, pause bool) (paused bool, err error)
	// CancelInstance cancels a queued or running instance.
	CancelInstance(ctx context.Context, instanceID string) error
	// RetryInstance re-runs a finished instance and returns the new ID.
	RetryInstance(ctx context.Context, id string) (instanceID string, err error)
	// TypeCatalog returns the action and requirement types for editors.
	TypeCatalog(ctx context.Context) (actions, requirements []ActionTypeInfo, err error)
	// Import reads commands from a document and returns the results.
	Import(ctx context.Context, document, format string) ([]ImportResult, error)
	// Export writes commands to a document (and format).
	Export(ctx context.Context, ids []string, formatOpt string) (document, format string, err error)
}

// commandService implements the CommandService handler.
type commandService struct {
	c Commands
}

func (s *commandService) summary(c CommandSummary) *v1alpha1.CommandSummary {
	return &v1alpha1.CommandSummary{
		Id:        c.ID,
		Name:      c.Name,
		Kind:      c.Kind,
		Enabled:   c.Enabled,
		Triggers:  c.Triggers,
		EventType: c.EventType,
		Channel:   c.Channel,
		Group:     c.Group,
		UpdatedAt: timestamppb.New(c.UpdatedAt),
	}
}

func (s *commandService) instance(i CommandInstance) *v1alpha1.CommandInstanceView {
	v := &v1alpha1.CommandInstanceView{
		InstanceId:  i.InstanceID,
		CommandId:   i.CommandID,
		CommandName: i.CommandName,
		State:       i.State,
		Source:      i.Source,
		Channel:     i.Channel,
		Error:       i.Error,
	}
	if !i.StartedAt.IsZero() {
		v.StartedAt = timestamppb.New(i.StartedAt)
	}
	if !i.EndedAt.IsZero() {
		v.EndedAt = timestamppb.New(i.EndedAt)
	}
	return v
}

func (s *commandService) List(ctx context.Context, req *v1alpha1.ListCommandsRequest) (*v1alpha1.ListCommandsResponse, error) {
	m := req
	list, err := s.c.List(ctx, m.Kind, m.Enabled, m.Prefix)
	if err != nil {
		return nil, apiError(err)
	}
	out := &v1alpha1.ListCommandsResponse{}
	for _, c := range list {
		out.Commands = append(out.Commands, s.summary(c))
	}
	return out, nil
}

func (s *commandService) Get(ctx context.Context, req *v1alpha1.GetCommandRequest) (*v1alpha1.GetCommandResponse, error) {
	if req.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "id is required")
	}
	sum, document, format, err := s.c.Get(ctx, req.Id)
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.GetCommandResponse{
		Summary:  s.summary(sum),
		Document: document,
		Format:   format,
	}, nil
}

func (s *commandService) Create(ctx context.Context, req *v1alpha1.CreateCommandRequest) (*v1alpha1.CreateCommandResponse, error) {
	m := req
	if m.Document == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "document is required")
	}
	summary, warnings, err := s.c.Create(ctx, m.Document, m.Format)
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.CreateCommandResponse{Command: s.summary(summary), Warnings: warnings}, nil
}

func (s *commandService) Update(ctx context.Context, req *v1alpha1.UpdateCommandRequest) (*v1alpha1.UpdateCommandResponse, error) {
	m := req
	if m.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "id is required")
	}
	summary, warnings, err := s.c.Update(ctx, m.Id, m.Document, m.Format)
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.UpdateCommandResponse{Command: s.summary(summary), Warnings: warnings}, nil
}

// SetEnabled switches the active switch of a command.
func (s *commandService) SetEnabled(ctx context.Context, req *v1alpha1.SetEnabledCommandRequest) (*v1alpha1.SetEnabledCommandResponse, error) {
	if req.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "id is required")
	}
	cmd, err := s.c.SetEnabled(ctx, req.Id, req.Enabled)
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.SetEnabledCommandResponse{Command: s.summary(cmd)}, nil
}

func (s *commandService) Delete(ctx context.Context, req *v1alpha1.DeleteCommandRequest) (*v1alpha1.DeleteCommandResponse, error) {
	if req.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "id is required")
	}
	if err := s.c.Delete(ctx, req.Id); err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.DeleteCommandResponse{}, nil
}

func (s *commandService) Run(ctx context.Context, req *v1alpha1.RunCommandRequest) (*v1alpha1.RunCommandResponse, error) {
	m := req
	if m.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "id is required")
	}
	instanceID, result, reason, err := s.c.Run(ctx, m.Id, m.Args, m.Channel)
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.RunCommandResponse{InstanceId: instanceID, Result: result, Reason: reason}, nil
}

func (s *commandService) ListInstances(ctx context.Context, req *v1alpha1.ListInstancesRequest) (*v1alpha1.ListInstancesResponse, error) {
	m := req
	paused, instances, err := s.c.ListInstances(ctx, m.CommandId, m.States, m.Limit)
	if err != nil {
		return nil, apiError(err)
	}
	out := &v1alpha1.ListInstancesResponse{Paused: paused}
	for _, i := range instances {
		out.Instances = append(out.Instances, s.instance(i))
	}
	return out, nil
}

func (s *commandService) PauseQueue(ctx context.Context, req *v1alpha1.PauseQueueRequest) (*v1alpha1.PauseQueueResponse, error) {
	paused, err := s.c.PauseQueue(ctx, req.Pause)
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.PauseQueueResponse{Paused: paused}, nil
}

func (s *commandService) CancelInstance(ctx context.Context, req *v1alpha1.CancelInstanceRequest) (*v1alpha1.CancelInstanceResponse, error) {
	if req.InstanceId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "instance_id is required")
	}
	if err := s.c.CancelInstance(ctx, req.InstanceId); err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.CancelInstanceResponse{}, nil
}

func (s *commandService) RetryInstance(ctx context.Context, req *v1alpha1.RetryInstanceRequest) (*v1alpha1.RetryInstanceResponse, error) {
	if req.InstanceId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "instance_id is required")
	}
	instanceID, err := s.c.RetryInstance(ctx, req.InstanceId)
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.RetryInstanceResponse{InstanceId: instanceID}, nil
}

func (s *commandService) TypeCatalog(ctx context.Context, _ *v1alpha1.TypeCatalogRequest) (*v1alpha1.TypeCatalogResponse, error) {
	actions, requirements, err := s.c.TypeCatalog(ctx)
	if err != nil {
		return nil, apiError(err)
	}
	out := &v1alpha1.TypeCatalogResponse{}
	for _, a := range actions {
		out.Actions = append(out.Actions, s.typeInfo(a))
	}
	for _, r := range requirements {
		out.Requirements = append(out.Requirements, s.typeInfo(r))
	}
	return out, nil
}

func (s *commandService) typeInfo(t ActionTypeInfo) *v1alpha1.ActionTypeInfo {
	return &v1alpha1.ActionTypeInfo{
		Type:          t.Type,
		Version:       t.Version,
		Category:      t.Category,
		Kinds:         t.Kinds,
		IsRequirement: t.IsRequirement,
		Schema:        t.Schema,
	}
}

func (s *commandService) Import(ctx context.Context, req *v1alpha1.ImportCommandsRequest) (*v1alpha1.ImportCommandsResponse, error) {
	m := req
	if m.Document == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "document is required")
	}
	results, err := s.c.Import(ctx, m.Document, m.Format)
	if err != nil {
		return nil, apiError(err)
	}
	out := &v1alpha1.ImportCommandsResponse{}
	for _, r := range results {
		out.Results = append(out.Results, &v1alpha1.ImportedCommand{Name: r.Name, Id: r.ID, Created: r.Created, Error: r.Error})
	}
	return out, nil
}

func (s *commandService) Export(ctx context.Context, req *v1alpha1.ExportCommandsRequest) (*v1alpha1.ExportCommandsResponse, error) {
	document, format, err := s.c.Export(ctx, req.Ids, req.Format)
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.ExportCommandsResponse{Document: document, Format: format}, nil
}
