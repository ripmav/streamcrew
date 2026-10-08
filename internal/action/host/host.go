// SPDX-License-Identifier: MIT

// Package host has the action types that act on the computer of the core
// (spec actions.md, B100 to B117): external_program starts programs. They
// belong to the category "host" (Code-ADR-0013) and need a host capability
// (ADR-0013), which the engine checks before every action (B7).
package host

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/template"
)

// Opener opens a path with the program the operating system assigns to it
// (actions.md B116). SystemOpener returns the one of the operating system;
// tests use a fake.
type Opener interface {
	// Open opens path in a program of its own and returns when it has
	// started; env is the environment of that program.
	Open(ctx context.Context, path string, env []string) error
}

// Ports are what the host types need.
type Ports struct {
	// Templates renders paths and arguments.
	Templates *template.Engine
	// Env is the environment of the programs the actions start: that of the
	// core without STREAMCREW_* (config.ProgramEnv, B117). It must not be
	// nil, because a nil environment would let programs inherit the whole
	// environment of the core; an empty one is allowed.
	Env []string
	// Opener opens paths with the program the system assigns to them.
	Opener Opener
	// Logger records programs that were started and output that was cut.
	Logger *slog.Logger
}

// ports are the ports of the host types.
type ports struct {
	Ports
}

// env returns a copy of the environment of programs; it is never nil.
func (p *ports) env() []string {
	return append([]string{}, p.Env...)
}

// Descriptors returns the host types with their ports.
func Descriptors(p Ports) ([]action.Descriptor, error) {
	switch {
	case p.Templates == nil:
		return nil, errors.New("host action types: no template engine")
	case p.Env == nil:
		return nil, errors.New("host action types: no environment for programs")
	case p.Opener == nil:
		return nil, errors.New("host action types: no opener")
	case p.Logger == nil:
		return nil, errors.New("host action types: no logger")
	}
	ports := &ports{Ports: p}
	return []action.Descriptor{
		action.Descriptor{
			Type:         TypeExternalProgram,
			Version:      1,
			Category:     action.CategoryHost,
			Capabilities: []capability.Capability{capability.HostProcess},
			Results:      []string{ResultOutput},
			Schema:       programSchema(),
		}.WithKinds(ProgramStart, func(k ProgramKind) (ExternalProgram, bool) {
			p := ExternalProgram{Common: action.On(), Kind: k, ports: ports}
			switch k {
			case ProgramStart:
				p.Launch = &LaunchOptions{}
			case ProgramRun:
				p.Launch = &LaunchOptions{}
				p.Wait = &WaitOptions{Timeout: action.Fixed(DefaultTimeout)}
			case ProgramOpen:
				p.Open = &OpenOptions{}
			default:
				return ExternalProgram{}, false
			}
			return p, true
		}),
	}, nil
}
