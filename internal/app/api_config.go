// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/ripmav/streamcrew/internal/api"
	"github.com/ripmav/streamcrew/internal/config"
)

// apiConfig adapts the core to the api.Config port (roadmap 6.6.2).
type apiConfig struct{ a *App }

// Get returns the current configuration document and the categories of
// its settings; live is true only when Update applies the change at
// runtime.
func (c apiConfig) Get() (string, []string, []string, []string, bool, error) {
	cfg := c.a.cfgView()
	doc, err := yaml.Marshal(cfg.View())
	if err != nil {
		return "", nil, nil, nil, false, err
	}
	var fixed []string
	if f := c.a.file; f != nil {
		for _, flag := range slices.Concat(config.Hot(), config.RestartOnly()) {
			if !f.FromFile(flag) {
				fixed = append(fixed, config.FileKey(flag))
			}
		}
	}
	return string(doc), config.Hot(), fixed, config.RestartOnly(), c.a.watcher != nil && cfg.Mode == config.ModeServer, nil
}

// Update validates and stores the document; the watcher applies the hot
// changes on its next comparison.
func (c apiConfig) Update(ctx context.Context, document string) ([]string, []string, string, error) {
	a := c.a
	if a.watcher == nil || a.cfgView().Mode != config.ModeServer {
		return nil, nil, "", api.ErrConfigReadOnly
	}
	applied, restart, stored, err := a.watcher.Update(ctx, []byte(document))
	if err != nil {
		return nil, nil, "", api.ErrConfigInvalid
	}
	return applied, restart, string(stored), nil
}
