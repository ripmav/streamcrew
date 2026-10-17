// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
)

// ErrConfigReadOnly reports that the live configuration is not available:
// the core runs without a configuration file or in a mode that does not
// apply file changes (roadmap 6.6.2).
var ErrConfigReadOnly = errors.New("the live configuration is not available in this mode")

// ErrConfigInvalid reports that a document is not a valid configuration.
var ErrConfigInvalid = errors.New("invalid configuration document")

// Config manages the configuration of the core (roadmap 6.6.2).
type Config interface {
	// Get returns the current configuration document and the categories
	// of its settings; live is false when Update is not possible.
	Get() (document string, hot, fixed, restart []string, live bool, err error)
	// Update validates the document, stores it in the configuration
	// file and returns the settings that change at runtime (applied)
	// and the settings that apply after a restart.
	Update(ctx context.Context, document string) (applied, restart []string, stored string, err error)
}

// configService implements streamcrewv1alpha1.ConfigServiceHandler.
type configService struct {
	c Config
}

// Get returns the current configuration document and the categories of
// its settings.
func (cs *configService) Get(_ context.Context, _ *connect.Request[v1alpha1.GetConfigRequest]) (*connect.Response[v1alpha1.GetConfigResponse], error) {
	document, hot, fixed, restart, live, err := cs.c.Get()
	if err != nil {
		return nil, configError(err)
	}
	resp := &v1alpha1.GetConfigResponse{
		Document: document,
		Hot:      hot,
		Fixed:    fixed,
		Restart:  restart,
		Live:     live,
	}
	return connect.NewResponse(resp), nil
}

// Update replaces the configuration document; an invalid document is
// rejected without a change.
func (cs *configService) Update(ctx context.Context, req *connect.Request[v1alpha1.UpdateConfigRequest]) (*connect.Response[v1alpha1.UpdateConfigResponse], error) {
	applied, restart, stored, err := cs.c.Update(ctx, req.Msg.GetDocument())
	if err != nil {
		return nil, configError(err)
	}
	resp := &v1alpha1.UpdateConfigResponse{
		Applied:  applied,
		Restart:  restart,
		Document: stored,
	}
	return connect.NewResponse(resp), nil
}

// configError maps the errors of the port to protocol codes.
func configError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrConfigReadOnly):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, ErrConfigInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}
