// SPDX-License-Identifier: MIT

package api

import (
	"context"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
)

// systemService implements streamcrewv1alpha1.SystemServiceHandler.
type systemService struct {
	s *Server
}

// Status reports the core version, mode, active profile and the connected
// platforms.
func (sys *systemService) Status(ctx context.Context, _ *v1alpha1.StatusRequest) (*v1alpha1.StatusResponse, error) {
	resp := &v1alpha1.StatusResponse{
		Version:   sys.s.cfg.Version,
		Mode:      sys.s.cfg.Mode,
		Profile:   sys.s.cfg.Profile,
		StartedAt: timestamppb.New(sys.s.cfg.StartedAt),
	}
	if p := sys.s.cfg.Platforms; p != nil {
		for _, st := range p.Status(ctx) {
			resp.Platforms = append(resp.Platforms, &v1alpha1.PlatformStatus{
				Platform:  st.Name,
				Connected: st.Connected,
				Login:     st.Login,
				Live:      st.Live,
			})
		}
	}
	return resp, nil
}

// ListProfiles lists the profiles of the core and names the active one.
func (sys *systemService) ListProfiles(ctx context.Context, _ *v1alpha1.ListProfilesRequest) (*v1alpha1.ListProfilesResponse, error) {
	resp := &v1alpha1.ListProfilesResponse{}
	if p := sys.s.cfg.Profiles; p != nil {
		profiles, active, err := p.List(ctx)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		for _, prof := range profiles {
			resp.Profiles = append(resp.Profiles, &v1alpha1.Profile{Name: prof.Name, Path: prof.Path})
		}
		resp.Active = active
	}
	return resp, nil
}

// Shutdown stops the core.
func (sys *systemService) Shutdown(_ context.Context, _ *v1alpha1.ShutdownRequest) (*v1alpha1.ShutdownResponse, error) {
	if sys.s.cfg.Shutdown != nil {
		sys.s.cfg.Shutdown()
	}
	return &v1alpha1.ShutdownResponse{}, nil
}
