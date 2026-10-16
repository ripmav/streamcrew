// SPDX-License-Identifier: MIT

package api

import (
	"context"

	"connectrpc.com/connect"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
)

// Settings manages the settings sections of a profile (roadmap 6.2.1, API).
// The service's own methods satisfy this port directly.
type Settings interface {
	// Sections returns the names of the known settings sections.
	Sections() []string
	// Document returns the stored document of the section; found is false if
	// the section was never saved.
	Document(ctx context.Context, name string) (doc []byte, found bool, err error)
	// DefaultDocument returns the encoded defaults of the section.
	DefaultDocument(name string) ([]byte, error)
	// SetDocument validates the document against the section and stores it.
	SetDocument(ctx context.Context, name string, doc []byte) error
}

// settingsService implements streamcrewv1alpha1.SettingsServiceHandler.
type settingsService struct {
	s Settings
}

// ListSections lists the names of the settings sections.
func (ss *settingsService) ListSections(_ context.Context, _ *connect.Request[v1alpha1.ListSectionsRequest]) (*connect.Response[v1alpha1.ListSectionsResponse], error) {
	resp := &v1alpha1.ListSectionsResponse{}
	for _, name := range ss.s.Sections() {
		resp.Sections = append(resp.Sections, &v1alpha1.Section{Name: name})
	}
	return connect.NewResponse(resp), nil
}

// Get returns a settings section as its document; a section that was never
// saved is returned as its defaults.
func (ss *settingsService) Get(ctx context.Context, req *connect.Request[v1alpha1.GetSettingsRequest]) (*connect.Response[v1alpha1.GetSettingsResponse], error) {
	name := req.Msg.GetName()
	doc, found, err := ss.s.Document(ctx, name)
	if err != nil {
		return nil, apiError(err)
	}
	if !found {
		doc, err = ss.s.DefaultDocument(name)
		if err != nil {
			return nil, apiError(err)
		}
	}
	return connect.NewResponse(&v1alpha1.GetSettingsResponse{Name: name, Document: string(doc)}), nil
}

// Update replaces a settings section with its new document.
func (ss *settingsService) Update(ctx context.Context, req *connect.Request[v1alpha1.UpdateSettingsRequest]) (*connect.Response[v1alpha1.UpdateSettingsResponse], error) {
	m := req.Msg
	if err := ss.s.SetDocument(ctx, m.GetName(), []byte(m.GetDocument())); err != nil {
		return nil, apiError(err)
	}
	return connect.NewResponse(&v1alpha1.UpdateSettingsResponse{Name: m.GetName(), Document: m.GetDocument()}), nil
}
