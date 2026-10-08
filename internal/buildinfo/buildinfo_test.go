// SPDX-License-Identifier: MIT

package buildinfo_test

import (
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/buildinfo"
)

func TestFromBuildInfo(t *testing.T) {
	t.Parallel()
	platform := runtime.GOOS + "/" + runtime.GOARCH

	tests := []struct {
		name string
		bi   *debug.BuildInfo
		want buildinfo.Info
	}{
		{
			name: "no build info",
			bi:   nil,
			want: buildinfo.Info{Version: buildinfo.DevelVersion, GoVersion: runtime.Version(), Platform: platform},
		},
		{
			name: "release with VCS data",
			bi: &debug.BuildInfo{
				GoVersion: "go1.27.1",
				Main:      debug.Module{Version: "v0.1.0"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "0123456789abcdef0123"},
					{Key: "vcs.time", Value: "2026-09-29T10:00:00Z"},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			want: buildinfo.Info{
				Version:   "v0.1.0",
				Revision:  "0123456789abcdef0123",
				Time:      "2026-09-29T10:00:00Z",
				GoVersion: "go1.27.1",
				Platform:  platform,
			},
		},
		{
			name: "modified working tree without module version",
			bi: &debug.BuildInfo{
				GoVersion: "go1.27.1",
				Settings:  []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}},
			},
			want: buildinfo.Info{Version: buildinfo.DevelVersion, Modified: true, GoVersion: "go1.27.1", Platform: platform},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, buildinfo.FromBuildInfo(tc.bi))
		})
	}
}

func TestInfoString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info buildinfo.Info
		want string
	}{
		{
			name: "full",
			info: buildinfo.Info{
				Version:   "v0.1.0",
				Revision:  "0123456789abcdef0123",
				Time:      "2026-09-29T10:00:00Z",
				Modified:  true,
				GoVersion: "go1.27.1",
				Platform:  "linux/amd64",
			},
			want: "v0.1.0 (0123456789ab-dirty, 2026-09-29T10:00:00Z, go1.27.1, linux/amd64)",
		},
		{
			name: "devel without VCS data",
			info: buildinfo.Info{Version: buildinfo.DevelVersion, GoVersion: "go1.27.1", Platform: "darwin/arm64"},
			want: "(devel) (go1.27.1, darwin/arm64)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.info.String())
		})
	}
}

func TestRead(t *testing.T) {
	t.Parallel()
	info := buildinfo.Read()
	assert.NotEmpty(t, info.Version)
	assert.Equal(t, runtime.GOOS+"/"+runtime.GOARCH, info.Platform)
}
