// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
)

// MaxCallDepth is how deep calls of other commands may nest (B73).
const MaxCallDepth = 10

var (
	// ErrInvalidCall is returned for call options that contradict each
	// other.
	ErrInvalidCall = errors.New("invalid call options")
	// ErrCallCycle is returned for a call of a command that is already in
	// the chain of calls (B73).
	ErrCallCycle = errors.New("the command is already in the chain of calls")
	// ErrCallDepth is returned for a call deeper than MaxCallDepth (B73).
	ErrCallDepth = errors.New("calls nested too deep")
	// ErrCallFailed is returned when a called command that the caller waits
	// for fails or is canceled (B36).
	ErrCallFailed = errors.New("the called command did not complete")
)

// CallOptions configure a call of another command (B30). The command action
// (roadmap 3.3) sets them from its configuration, in which waiting is the
// default.
type CallOptions struct {
	// Wait runs the command as part of the calling instance and returns
	// when it has ended (B31). Without it, the command is queued like a
	// triggered one, with locks and pause (B32).
	Wait bool
	// CheckRequirements checks the requirements of the called command with
	// the user of the calling instance (B33).
	CheckRequirements bool
	// OwnArgs gives the called command Args instead of the arguments of the
	// calling instance (B34).
	OwnArgs bool
	// Args are the arguments of the called command if OwnArgs is set; the
	// text after the trigger is them joined by spaces. Without OwnArgs,
	// Args must be empty.
	Args []string
}

// Call starts another command from the running instance; the command action
// (roadmap 3.3) uses it. The called command runs with the user, platform,
// target and message of this instance (B34), in its current version. It
// must not be in the chain of calls already, and the chain must not get
// deeper than MaxCallDepth (B73).
//
// With Wait, the command runs as part of this instance: without locks,
// also during a pause, sharing the values of the run (B31, B35); it ends
// with this instance (B52). Without Wait, it is queued with a copy of the
// values (B32, B35).
//
// The result says what became of the command: OutcomeCompleted with Wait,
// OutcomeQueued without, or OutcomeDisabled, OutcomeRejected or
// OutcomeWaiting as for Trigger. Call returns an error if it could not
// handle the call, and ErrCallFailed if a command it waited for failed or
// was canceled (B36).
func (r *Run) Call(ctx context.Context, commandID id.ID, opts CallOptions) (Result, error) {
	if !opts.OwnArgs && len(opts.Args) > 0 {
		return Result{}, fmt.Errorf("call command %s: %w: arguments without OwnArgs", commandID, ErrInvalidCall)
	}
	caller := r.in
	if slices.Contains(caller.chain, commandID) {
		return Result{}, fmt.Errorf("call command %s: %w", commandID, ErrCallCycle)
	}
	if len(caller.chain) > MaxCallDepth {
		return Result{}, fmt.Errorf("call command %s: %w (%d)", commandID, ErrCallDepth, MaxCallDepth)
	}
	e := r.engine
	cmd, err := e.commands.Command(ctx, commandID)
	if err != nil {
		return Result{}, fmt.Errorf("call command %s: %w", commandID, err)
	}
	p := caller.params
	p.Values = caller.scope.Values()
	if opts.OwnArgs {
		p.Args, p.ArgsText = opts.Args, strings.Join(opts.Args, " ")
	}
	if err := checkRun(cmd, p); err != nil {
		return Result{}, fmt.Errorf("call command %q: %w", cmd.Name, err)
	}
	if !cmd.Enabled {
		return Result{Outcome: OutcomeDisabled}, nil // B14
	}
	cfg, err := e.readConfig(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("call command %q: %w", cmd.Name, err)
	}

	d := Met(p)
	if opts.CheckRequirements {
		if d, err = e.decide(ctx, cmd, p); err != nil {
			return Result{}, fmt.Errorf("call command %q: %w", cmd.Name, err)
		}
	}
	switch d.Verdict {
	case VerdictWaiting:
		return Result{Outcome: OutcomeWaiting}, nil
	case VerdictRejected:
		e.reject(ctx, cmd, p, d.Rejection, cfg.Commands)
		return Result{Outcome: OutcomeRejected, Rejection: d.Rejection}, nil
	case VerdictMet:
	}
	if opts.CheckRequirements {
		e.resetErrorCooldowns(cmd.ID) // B13
	}

	org := origin{parent: caller.id, chain: caller.chain}
	if opts.Wait {
		// The called actions have their own time limits; the one of the
		// calling action stands meanwhile (actions.md B8).
		if f := r.top(); f != nil {
			f.stand()
			defer f.run()
		}
		res := Result{Outcome: OutcomeCompleted, Instances: make([]id.ID, 0, len(d.Runs))}
		for _, run := range d.Runs {
			instanceID, err := e.runCall(ctx, r, cmd, run, org)
			if err != nil {
				return Result{}, err
			}
			res.Instances = append(res.Instances, instanceID)
		}
		return res, nil
	}
	res := Result{Outcome: OutcomeQueued, Instances: make([]id.ID, 0, len(d.Runs))}
	var dropErr error
	for _, run := range d.Runs {
		instanceID, err := e.enqueue(ctx, cmd, SourceCall, run, cfg, admission{}, org)
		if err != nil {
			dropErr = errors.Join(dropErr, err)
			res.Dropped++
			continue
		}
		res.Instances = append(res.Instances, instanceID)
	}
	if len(res.Instances) == 0 {
		return Result{}, dropErr
	}
	return res, nil
}

// runCall runs cmd as part of the calling instance (B31) and returns the ID
// of its instance.
func (e *Engine) runCall(ctx context.Context, caller *Run, cmd command.Command, p Params, org origin) (id.ID, error) {
	in := newInstance(cmd, SourceCall, withTarget(p), Config{}, []string{}, org)
	// It belongs to a greeting that calls it (B43).
	in.greeting, in.mediaGap = caller.in.greeting, caller.in.mediaGap
	in.scope = caller.in.scope.Share()
	in.scope.CommandName = cmd.Name
	in.scope.Args, in.scope.ArgsText = p.Args, p.ArgsText
	// The call ends with the caller (B52) or on its own.
	cctx, cancel := context.WithCancel(ctx)
	in.cancel = cancel

	e.mu.Lock()
	// A call the caller waits for does not wait in the queue, and it runs
	// while the core stops, as part of its caller.
	if err := e.admitLocked(ctx, cmd, SourceCall, admission{reserved: true, whileStopping: true}); err != nil {
		e.mu.Unlock()
		cancel()
		return id.ID{}, err
	}
	e.addLocked(ctx, in)
	in.state = StateRunning
	in.startedAt = time.Now()
	e.publishLocked(ctx, TypeInstanceStarted, in.snapshot())
	e.mu.Unlock()

	e.execute(cctx, in)

	e.mu.Lock()
	state := in.state
	e.mu.Unlock()
	if state != StateCompleted {
		return in.id, fmt.Errorf("call command %q: %w: %s", cmd.Name, ErrCallFailed, state)
	}
	return in.id, nil
}
