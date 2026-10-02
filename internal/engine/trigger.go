// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/i18n"
	"github.com/ripmav/streamcrew/internal/settings"
)

var (
	// ErrInvalidSource is returned by Trigger for a source that is not
	// automatic.
	ErrInvalidSource = errors.New("not an automatic source")
	// ErrInvalidDecision is returned when the requirement service returns
	// a decision that contradicts itself.
	ErrInvalidDecision = errors.New("invalid requirement decision")
)

// Verdict says what the requirements of a command decided for a run (B10,
// B11, B82).
type Verdict string

// Verdicts of the requirements.
const (
	// VerdictMet means all requirements are met: their costs are charged,
	// their cooldowns started, and Decision.Runs are queued.
	VerdictMet Verdict = "met"
	// VerdictWaiting means nothing runs yet and nothing is charged, e.g.
	// below a threshold (commands.md, B46). It is not a rejection.
	VerdictWaiting Verdict = "waiting"
	// VerdictRejected means a requirement is not met and nothing is
	// charged; Decision.Rejection says which.
	VerdictRejected Verdict = "rejected"
)

// Decision is what the requirements of a command decided for a run.
type Decision struct {
	Verdict Verdict
	// Runs are the runs to queue for VerdictMet, at least one: the run
	// itself, or one run per user for a threshold that runs the command
	// for each of them (B82). They are empty for the other verdicts.
	Runs []Params
	// Rejection is the unmet requirement for VerdictRejected; the zero
	// value for the other verdicts.
	Rejection Rejection
	// Revert takes back the costs and cooldowns the requirements applied
	// for VerdictMet. The engine calls it if it queues none of the runs,
	// e.g. because the core stops meanwhile or, for a call, the queue is
	// full (B15). It is nil if there is nothing to take back, and for the
	// other verdicts.
	Revert func(ctx context.Context) error
}

// Met returns the decision that all requirements are met and runs are to
// be queued.
func Met(runs ...Params) Decision {
	return Decision{Verdict: VerdictMet, Runs: runs}
}

// Waiting returns the decision that nothing runs yet.
func Waiting() Decision {
	return Decision{Verdict: VerdictWaiting}
}

// Rejected returns the decision that a requirement is not met.
func Rejected(r Rejection) Decision {
	return Decision{Verdict: VerdictRejected, Rejection: r}
}

// validate checks that d does not contradict itself.
func (d Decision) validate() error {
	switch d.Verdict {
	case VerdictMet:
		if len(d.Runs) == 0 {
			return fmt.Errorf("%w: met without a run", ErrInvalidDecision)
		}
		for _, run := range d.Runs {
			if err := checkParams(run); err != nil {
				return fmt.Errorf("%w: %w", ErrInvalidDecision, err)
			}
		}
	case VerdictWaiting, VerdictRejected:
		if len(d.Runs) > 0 {
			return fmt.Errorf("%w: %s with runs", ErrInvalidDecision, d.Verdict)
		}
		if d.Revert != nil {
			return fmt.Errorf("%w: %s with something to take back", ErrInvalidDecision, d.Verdict)
		}
	default:
		return fmt.Errorf("%w: unknown verdict %q", ErrInvalidDecision, d.Verdict)
	}
	rejected := d.Verdict == VerdictRejected
	if rejected == d.Rejection.zero() {
		return fmt.Errorf("%w: %s with a rejection that does not fit", ErrInvalidDecision, d.Verdict)
	}
	if rejected && (d.Rejection.Requirement == "" || d.Rejection.Reason.Key == "") {
		return fmt.Errorf("%w: rejection without requirement or reason", ErrInvalidDecision)
	}
	return nil
}

// Rejection is a requirement that is not met (B11).
type Rejection struct {
	// Requirement is the requirement type, e.g. "cooldown".
	Requirement string
	// Reason says why, as a message the requirement service renders in the
	// language of the profile when it tells the user (ADR-0022, point 5);
	// it is required.
	Reason i18n.Message
	// Tell says whether the user is told the reason (B11), subject to the
	// error cooldown (B12). The requirement service does not tell, e.g.,
	// in a run without a user.
	Tell bool
}

// zero reports whether r is the zero value, the rejection of a decision
// that is not a rejection.
func (r Rejection) zero() bool {
	return r.Requirement == "" && r.Reason.Key == "" && r.Reason.Args == nil && !r.Tell
}

// Requirements checks and applies the requirements of commands (B10). The
// requirement service (roadmap 3.4) implements it.
type Requirements interface {
	// Apply checks the requirements of cmd for the run p. Only for
	// VerdictMet it charges their costs and starts their cooldowns, which
	// Decision.Revert takes back. Decisions about the same command are
	// made one after another (requirements.md, B3). An error means it
	// could not decide.
	Apply(ctx context.Context, cmd command.Command, p Params) (Decision, error)
	// Notify tells the user of p the reason of r (B11). The engine calls it
	// only for a rejection with Tell, outside the error cooldown (B12).
	Notify(ctx context.Context, cmd command.Command, p Params, r Rejection) error
	// StartCooldown starts the cooldown of cmd as if cmd had just been
	// queued for the run p, by the kind of its cooldown requirement; for the
	// kinds per user for the user of p (actions.md B37). Without a cooldown
	// requirement it does nothing. For a cooldown per user and a run without
	// a user it returns an error.
	StartCooldown(ctx context.Context, cmd command.Command, p Params) error
}

// Users finds the user an argument names (B81); the user service
// implements it, as it does template.Users.
type Users interface {
	// UserByName finds the user with the login name on platform p,
	// regardless of case; ok is false if there is none.
	UserByName(ctx context.Context, p platform.Name, name string) (u user.User, ok bool, err error)
}

// WithRequirements sets the requirement service; without it, the engine
// checks no requirements, and every run is met.
func WithRequirements(r Requirements) Option {
	return func(e *Engine) error {
		if r == nil {
			return fmt.Errorf("%w: nil requirements", ErrInvalidOption)
		}
		e.requirements = r
		return nil
	}
}

// WithUsers sets where the target of a run is looked up (B81); without it,
// the engine looks up no target from the arguments.
func WithUsers(u Users) Option {
	return func(e *Engine) error {
		if u == nil {
			return fmt.Errorf("%w: nil users", ErrInvalidOption)
		}
		e.users = u
		return nil
	}
}

// Request asks to run a command automatically, e.g. for a chat message
// that matched a trigger.
type Request struct {
	// Command is the command in its current version.
	Command command.Command
	// Source is SourceChat, SourceEvent or SourceTimer.
	Source Source
	// Params are the data of the run.
	Params Params
	// Event is the event type of an event command; empty for the other
	// sources.
	Event event.Type
	// Entrance marks the entrance command of a user, a greeting (B41).
	// Event commands of "chat.user.entrance" are greetings without it.
	Entrance bool
}

// Outcome says what became of a triggered or called command.
type Outcome string

// Outcomes of Trigger and Run.Call.
const (
	// OutcomeQueued means Result.Instances were queued.
	OutcomeQueued Outcome = "queued"
	// OutcomeCompleted means the called command ran as part of its caller
	// and completed; Result.Instances are its instances. Only Run.Call
	// with Wait has it.
	OutcomeCompleted Outcome = "completed"
	// OutcomeWaiting means the requirements wait, e.g. for a threshold
	// (B82); nothing was queued.
	OutcomeWaiting Outcome = "waiting"
	// OutcomeRejected means a requirement is not met (B11);
	// Result.Rejection says which.
	OutcomeRejected Outcome = "rejected"
	// OutcomeDisabled means the command is disabled (B14).
	OutcomeDisabled Outcome = "disabled"
)

// Result is what became of a triggered or called command. The outcomes are
// not errors; Trigger and Run.Call return an error only if they could not
// handle the request.
type Result struct {
	Outcome Outcome
	// Instances are the new instances for OutcomeQueued and
	// OutcomeCompleted, at least one; empty for the other outcomes.
	Instances []id.ID
	// Rejection is the unmet requirement for OutcomeRejected; the zero
	// value for the other outcomes.
	Rejection Rejection
	// Dropped counts the runs of a threshold that no longer fit into the
	// queue (B15); their costs stay charged (B53).
	Dropped int
}

// Trigger runs req.Command automatically if it is enabled and its
// requirements are met (B10 to B15, B82). A greeting is queued also while
// greetings are paused, and starts when they are resumed (B41). The result says what became
// of it. Trigger returns an error only if it could not handle the request:
// ErrInvalidSource, an invalid command or parameters, ErrQueueFull,
// ErrClosed while the core stops (except for event commands of
// "app.stopping", B55), unreadable settings, or a requirement service that
// failed or returned an invalid decision.
func (e *Engine) Trigger(ctx context.Context, req Request) (Result, error) {
	cmd := req.Command
	switch req.Source {
	case SourceChat, SourceEvent, SourceTimer:
	default:
		return Result{}, fmt.Errorf("trigger command %q: %w: %q", cmd.Name, ErrInvalidSource, req.Source)
	}
	if err := checkRun(cmd, req.Params); err != nil {
		return Result{}, fmt.Errorf("trigger command %q: %w", cmd.Name, err)
	}
	if !cmd.Enabled {
		return Result{Outcome: OutcomeDisabled}, nil
	}
	adm := admission{
		whileStopping: req.Source == SourceEvent && req.Event == eventtype.AppStopping,
		greeting:      req.Entrance || req.Source == SourceEvent && req.Event == eventtype.ChatUserEntrance,
	}

	e.mu.Lock()
	err := e.reserveLocked(ctx, cmd, req.Source, adm)
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	release := func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.reserved--
	}

	cfg, err := e.readConfig(ctx)
	if err != nil {
		release()
		return Result{}, fmt.Errorf("trigger command %q: %w", cmd.Name, err)
	}
	p := e.lookupTarget(ctx, req.Params)
	d, err := e.decide(ctx, cmd, p)
	if err != nil {
		release()
		return Result{}, fmt.Errorf("trigger command %q: %w", cmd.Name, err)
	}
	switch d.Verdict {
	case VerdictWaiting:
		release()
		return Result{Outcome: OutcomeWaiting}, nil
	case VerdictRejected:
		release()
		e.reject(ctx, cmd, p, d.Rejection, cfg.Commands)
		return Result{Outcome: OutcomeRejected, Rejection: d.Rejection}, nil
	case VerdictMet:
	}

	e.resetErrorCooldowns(cmd.ID) // B13
	res := Result{Outcome: OutcomeQueued, Instances: make([]id.ID, 0, len(d.Runs))}
	var dropErr error
	for i, run := range d.Runs {
		adm.reserved = i == 0
		instanceID, err := e.enqueue(ctx, cmd, req.Source, run, cfg, adm, origin{})
		if err != nil {
			dropErr = errors.Join(dropErr, err)
			res.Dropped++
			continue
		}
		res.Instances = append(res.Instances, instanceID)
	}
	if len(res.Instances) == 0 {
		e.revert(ctx, cmd, d)
		return Result{}, dropErr
	}
	if res.Dropped > 0 {
		e.logger.WarnContext(ctx, "runs of a threshold dropped",
			"command", cmd.Name, "dropped", res.Dropped, "error", dropErr)
	}
	return res, nil
}

// decide applies the requirements of cmd for p and checks the decision. It
// takes back what an invalid decision applied.
func (e *Engine) decide(ctx context.Context, cmd command.Command, p Params) (Decision, error) {
	if e.requirements == nil {
		return Met(p), nil
	}
	d, err := e.requirements.Apply(ctx, cmd, p)
	if err != nil {
		return Decision{}, fmt.Errorf("check requirements: %w", err)
	}
	if err := d.validate(); err != nil {
		e.revert(ctx, cmd, d)
		return Decision{}, err
	}
	return d, nil
}

// revert takes back what the requirements applied for d, after none of its
// runs could be queued (B15). It does so also if the request was canceled
// meanwhile; a failure is logged.
func (e *Engine) revert(ctx context.Context, cmd command.Command, d Decision) {
	if d.Revert == nil {
		return
	}
	if err := d.Revert(context.WithoutCancel(ctx)); err != nil {
		e.logger.ErrorContext(ctx, "taking back the requirements of a command that was not queued failed",
			"command", cmd.Name, "error", err)
	}
}

// reject tells the user why cmd did not run, if the rejection says so and
// the error cooldown lets the message go out (B11, B12).
func (e *Engine) reject(ctx context.Context, cmd command.Command, p Params, r Rejection, cfg settings.Commands) {
	e.logger.DebugContext(ctx, "requirement not met",
		"command", cmd.Name, "requirement", r.Requirement, "tell", r.Tell)
	if !r.Tell {
		return
	}
	if cfg.ErrorCooldown == settings.ErrorCooldownSilent {
		e.logger.DebugContext(ctx, "requirement not met, messages are off",
			"command", cmd.Name, "requirement", r.Requirement)
		return
	}
	send, err := e.takeErrorMessage(cmd.ID, r.Requirement, cfg)
	if err != nil {
		e.logger.ErrorContext(ctx, "error cooldown failed", "command", cmd.Name, "error", err)
		return
	}
	if !send {
		e.logger.InfoContext(ctx, "requirement not met, message held back by the error cooldown",
			"command", cmd.Name, "requirement", r.Requirement)
		return
	}
	if err := e.requirements.Notify(ctx, cmd, p, r); err != nil {
		e.logger.WarnContext(ctx, "telling the user about an unmet requirement failed",
			"command", cmd.Name, "requirement", r.Requirement, "error", err)
	}
}

// takeErrorMessage reports whether an error message about requirement of
// the command may go out now, and if so, starts the error cooldown (B12).
func (e *Engine) takeErrorMessage(commandID id.ID, requirement string, cfg settings.Commands) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	until := now.Add(cfg.ErrorCooldownDuration.Std())
	switch cfg.ErrorCooldown {
	case settings.ErrorCooldownOff:
		return true, nil
	case settings.ErrorCooldownGlobal:
		if now.Before(e.globalErrorUntil) {
			return false, nil
		}
		e.globalErrorUntil = until
		return true, nil
	case settings.ErrorCooldownPerCommand:
		m := e.errorUntil[commandID]
		if now.Before(m[requirement]) {
			return false, nil
		}
		if m == nil {
			m = make(map[string]time.Time)
			e.errorUntil[commandID] = m
		}
		m[requirement] = until
		return true, nil
	default:
		return false, fmt.Errorf("%w: unknown error cooldown %q", ErrInvalidConfig, cfg.ErrorCooldown)
	}
}

// resetErrorCooldowns lets the next error messages about the requirements
// of a command go out at once, after the command was queued (B13).
func (e *Engine) resetErrorCooldowns(commandID id.ID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.errorUntil, commandID)
}

// lookupTarget returns p with the user the first argument names, with or
// without "@", as its target if the caller set none and the platform knows
// the user (B81). Otherwise p is unchanged; the engine then takes the
// triggering user when it queues the run.
func (e *Engine) lookupTarget(ctx context.Context, p Params) Params {
	if p.Target != nil || e.users == nil || p.Platform == "" || len(p.Args) == 0 {
		return p
	}
	name := strings.TrimPrefix(p.Args[0], "@")
	if name == "" {
		return p
	}
	u, ok, err := e.users.UserByName(ctx, p.Platform, name)
	if err != nil {
		e.logger.WarnContext(ctx, "looking up the target user failed", "error", err)
		return p
	}
	if ok {
		p.Target = &u
	}
	return p
}
