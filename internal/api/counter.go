// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	"github.com/ripmav/streamcrew/internal/domain/counter"
	"github.com/ripmav/streamcrew/internal/store"
)

// Counter is a profile counter as seen through the API.
type Counter struct {
	// ID is the counter ID.
	ID string
	// Name is the unique counter name.
	Name string
	// Value is the current value, an exact decimal as a string.
	Value string
	// Step is the step size, an exact decimal as a string.
	Step string
	// ResetOnStart is the reset-on-start switch.
	ResetOnStart bool
	// UpdatedAt is when the counter was last changed.
	UpdatedAt time.Time
}

// Counters manages the counters of a profile (roadmap 6.2.1, API).
type Counters interface {
	List(ctx context.Context, prefix string) ([]Counter, error)
	Get(ctx context.Context, name string) (Counter, error)
	Create(ctx context.Context, name, value, step string, resetOnStart bool) (Counter, error)
	// Update changes the step (empty keeps it) and the reset-on-start switch
	// (nil keeps it) of the counter.
	Update(ctx context.Context, name, step string, resetOnStart *bool) (Counter, error)
	Set(ctx context.Context, name, value string) (Counter, error)
	// Add adds the amount (empty adds one step) to the counter.
	Add(ctx context.Context, name, amount string) (Counter, error)
	Reset(ctx context.Context, name string) (Counter, error)
	Delete(ctx context.Context, name string) error
}

// apiError maps an error of the application services to a Connect error with
// the matching gRPC status code.
func apiError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	case errors.Is(err, store.ErrConflict):
		return connect.NewError(connect.CodeAlreadyExists, err.Error()).WithCause(err)
	case errors.Is(err, ErrRestoreRunning):
		return connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	case errors.Is(err, counter.ErrInvalid), errors.Is(err, counter.ErrOverflow):
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
}

// counterService implements streamcrewv1alpha1.CounterServiceHandler.
type counterService struct {
	c Counters
}

// view converts a counter to its protocol message.
func (cs *counterService) view(c Counter) *v1alpha1.CounterView {
	return &v1alpha1.CounterView{
		Id:           c.ID,
		Name:         c.Name,
		Value:        c.Value,
		Step:         c.Step,
		ResetOnStart: c.ResetOnStart,
		UpdatedAt:    timestamppb.New(c.UpdatedAt),
	}
}

// List lists the counters.
func (cs *counterService) List(ctx context.Context, req *v1alpha1.ListCountersRequest) (*v1alpha1.ListCountersResponse, error) {
	counters, err := cs.c.List(ctx, req.GetPrefix())
	if err != nil {
		return nil, apiError(err)
	}
	resp := &v1alpha1.ListCountersResponse{}
	for _, c := range counters {
		resp.Counters = append(resp.Counters, cs.view(c))
	}
	return resp, nil
}

// Get returns one counter by name.
func (cs *counterService) Get(ctx context.Context, req *v1alpha1.GetCounterRequest) (*v1alpha1.GetCounterResponse, error) {
	c, err := cs.c.Get(ctx, req.GetName())
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.GetCounterResponse{Counter: cs.view(c)}, nil
}

// Create creates a counter.
func (cs *counterService) Create(ctx context.Context, req *v1alpha1.CreateCounterRequest) (*v1alpha1.CreateCounterResponse, error) {
	m := req
	c, err := cs.c.Create(ctx, m.GetName(), m.GetValue(), m.GetStep(), m.GetResetOnStart())
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.CreateCounterResponse{Counter: cs.view(c)}, nil
}

// Update changes the step and the reset-on-start switch of a counter.
func (cs *counterService) Update(ctx context.Context, req *v1alpha1.UpdateCounterRequest) (*v1alpha1.UpdateCounterResponse, error) {
	m := req
	c, err := cs.c.Update(ctx, m.GetName(), m.GetStep(), m.ResetOnStart)
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.UpdateCounterResponse{Counter: cs.view(c)}, nil
}

// Set sets the value of a counter.
func (cs *counterService) Set(ctx context.Context, req *v1alpha1.SetCounterRequest) (*v1alpha1.SetCounterResponse, error) {
	m := req
	c, err := cs.c.Set(ctx, m.GetName(), m.GetValue())
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.SetCounterResponse{Counter: cs.view(c)}, nil
}

// Add adds an amount to a counter.
func (cs *counterService) Add(ctx context.Context, req *v1alpha1.AddCounterRequest) (*v1alpha1.AddCounterResponse, error) {
	m := req
	c, err := cs.c.Add(ctx, m.GetName(), m.GetAmount())
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.AddCounterResponse{Counter: cs.view(c)}, nil
}

// Reset sets a counter to its reset value.
func (cs *counterService) Reset(ctx context.Context, req *v1alpha1.ResetCounterRequest) (*v1alpha1.ResetCounterResponse, error) {
	c, err := cs.c.Reset(ctx, req.GetName())
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.ResetCounterResponse{Counter: cs.view(c)}, nil
}

// Delete removes a counter.
func (cs *counterService) Delete(ctx context.Context, req *v1alpha1.DeleteCounterRequest) (*v1alpha1.DeleteCounterResponse, error) {
	if err := cs.c.Delete(ctx, req.GetName()); err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.DeleteCounterResponse{}, nil
}
