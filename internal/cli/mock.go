// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/commandfile"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/mock"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/store"
	"github.com/ripmav/streamcrew/internal/vault"
)

// mockCmd is the mock console (plan 7.2, roadmap 3.6): it runs the core in
// one process with the mock platform, against a copy of the profile that is
// discarded when the console ends, so that the original stays untouched.
type mockCmd struct {
	Commands []string `short:"c" type:"path" env:"-" placeholder:"FILE" help:"Files of commands as code (.yaml, .yml, .json), also directories, loaded into the copy of the profile before the core starts."`
	Script   string   `type:"existingfile" env:"-" placeholder:"FILE" help:"Read the console commands from this file instead of the standard input."`
	Streamer string   `env:"-" help:"Login name of the streamer account of the mock channel; empty for the default."`
	Bot      string   `env:"-" help:"Login name of the bot account of the mock channel; empty for none."`
	// appOpts are further options of the core; the tests use them, e.g. to
	// skip the system keyring.
	appOpts []app.Option
}

// consoleHelp is the text of the "help" command of the console.
const consoleHelp = `commands:
  chat send --as <login> <text>                  simulate a chat message of a user (unknown logins become new users)
  event simulate <type> [user <login>] [target <login>]
                                                 simulate a platform-neutral event, e.g. "channel.follow"
  stream start [title]                           start the stream of the mock channel
  stream stop                                    end the stream
  user add <login>                               add a user to the mock channel
  help                                           this text
  exit                                           stop the core and the console`

// errStop ends the console loop.
var errStop = errors.New("stop")

// Run starts the core with the mock platform on the copy of the profile and
// takes console commands from the standard input or from --script.
func (c mockCmd) Run(ctx context.Context, e *Env) (err error) {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	id, err := e.profileID(cfg)
	if err != nil {
		return err
	}
	profiles := e.profiles(cfg)
	if !profiles.Exists(id) {
		return fmt.Errorf("%w: %q", profile.ErrNotFound, id)
	}

	// The copy of the profile in a throw-away data directory, with the
	// vault key, so that the tokens of the copy decrypt like in the
	// original; the directory is deleted when the console ends.
	dir, err := os.MkdirTemp("", "streamcrew-mock-")
	if err != nil {
		return err
	}
	defer func() {
		if rmErr := os.RemoveAll(dir); rmErr != nil && err == nil {
			err = rmErr
		}
	}()
	rel, err := filepath.Rel(cfg.DataDir, profiles.Path(id))
	if err != nil {
		return err
	}
	dstPath := filepath.Join(dir, rel)
	if err := copyProfile(ctx, profiles.Path(id), dstPath); err != nil {
		return err
	}
	if key := filepath.Join(cfg.DataDir, vault.KeyFileName); fileExists(key) {
		if err := copyFile(key, filepath.Join(dir, vault.KeyFileName)); err != nil {
			return err
		}
	}

	// The files of commands as code, with the checks of the import, into
	// the copy, before the core starts.
	if len(c.Commands) > 0 {
		if err := c.importCommands(ctx, e, cfg, dir, dstPath); err != nil {
			return err
		}
	}

	runCfg := *cfg
	runCfg.DataDir = dir
	runCfg.Profile = id
	var mp *mock.Platform
	appOptions := []app.Option{
		app.WithConsole(e.Stderr),
		app.WithSecretKey(e.SecretKey),
		app.WithConfigFile(e.File),
		app.WithPlatform(func(_ context.Context, b app.PlatformBuilder) (connector.Platform, error) {
			var opts []mock.Option
			if c.Streamer != "" {
				opts = append(opts, mock.WithStreamer(c.Streamer))
			}
			if c.Bot != "" {
				opts = append(opts, mock.WithBot(c.Bot))
			}
			opts = append(opts, mock.WithLogger(b.Logger), mock.WithPublisher(b.Publisher))
			p, err := mock.New(b.Receiver, opts...)
			if err != nil {
				return nil, err
			}
			mp = p
			return p, nil
		}),
	}
	appOptions = append(appOptions, c.appOpts...)
	a, err := app.New(ctx, runCfg, appOptions...)
	if err != nil {
		return err
	}
	slog.SetDefault(a.Logger())

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Run(runCtx) }()

	// The console takes input when the core is ready, so that the input of
	// the console reaches a running platform; a failure of the core before
	// then stops the console.
	for !a.Ready() {
		select {
		case runErr := <-done:
			return &reportedError{err: runErr}
		case <-time.After(50 * time.Millisecond):
		}
	}
	streamer := mock.DefaultStreamer
	if c.Streamer != "" {
		streamer = c.Streamer
	}
	bot := "none"
	if c.Bot != "" {
		bot = c.Bot
	}
	fmt.Fprintf(e.Stdout, "profile %s (copy in %s)\n", id, dir)
	fmt.Fprintf(e.Stdout, "mock platform: streamer %s, bot %s\n", streamer, bot)

	in, err := consoleInput(c.Script)
	if err != nil {
		cancel()
		return errors.Join(err, <-done)
	}
	defer in.Close()
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := c.exec(ctx, e, mp, line); err != nil {
			if errors.Is(err, errStop) {
				break
			}
			if _, err2 := fmt.Fprintln(e.Stderr, "error:", err); err2 != nil {
				break
			}
		}
	}
	err = scanner.Err()
	cancel()
	return errors.Join(err, <-done)
}

// consoleInput returns the reader of the console commands: the file of
// --script or the standard input.
func consoleInput(script string) (io.ReadCloser, error) {
	if script == "" {
		return os.Stdin, nil
	}
	return os.Open(script)
}

// exec runs one line of the console.
func (c mockCmd) exec(ctx context.Context, e *Env, mp *mock.Platform, line string) error {
	fields := strings.Fields(line)
	switch fields[0] {
	case "exit", "quit":
		return errStop
	case "help", "?":
		_, err := fmt.Fprintln(e.Stdout, consoleHelp)
		return err
	case "chat":
		return c.execChat(ctx, e, mp, line)
	case "event":
		return c.execEvent(ctx, e, mp, fields)
	case "stream":
		return c.execStream(ctx, e, mp, line, fields)
	case "user":
		return c.execUser(e, mp, fields)
	default:
		return fmt.Errorf("unknown command %q; try \"help\"", fields[0])
	}
}

// execChat runs "chat send --as <login> <text>".
func (c mockCmd) execChat(ctx context.Context, e *Env, mp *mock.Platform, line string) error {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[1] != "send" {
		return errors.New("the form is \"chat send --as <login> <text>\"")
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, "chat send"))
	if !strings.HasPrefix(rest, "--as ") {
		return errors.New("the form is \"chat send --as <login> <text>\"")
	}
	after := rest[len("--as "):]
	before, after0, ok := strings.Cut(after, " ")
	if !ok {
		return errors.New("chat send needs a text after the login name")
	}
	login, text := before, strings.TrimSpace(after0)
	if text == "" {
		return errors.New("chat send needs a text after the login name")
	}
	sent, err := mp.Say(ctx, login, text)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.Stdout, "message %s: %s\n", sent.ID, sent.Delivery)
	return err
}

// execEvent runs "event simulate <type> [user <login>] [target <login>]".
func (c mockCmd) execEvent(ctx context.Context, e *Env, mp *mock.Platform, fields []string) error {
	if len(fields) < 3 || fields[1] != "simulate" {
		return errors.New("the form is \"event simulate <type> [user <login>] [target <login>]\"")
	}
	ve := mock.Event{Type: event.Type(fields[2])}
	for i := 3; i < len(fields); i++ {
		if i+1 >= len(fields) {
			return fmt.Errorf("event %q needs a login name", fields[i])
		}
		switch fields[i] {
		case "user":
			ve.User = fields[i+1]
		case "target":
			ve.Target = fields[i+1]
		default:
			return fmt.Errorf("unknown option %q; try \"help\"", fields[i])
		}
		i++
	}
	sent, err := mp.Simulate(ctx, ve)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.Stdout, "event %s: %s\n", sent.ID, sent.Delivery)
	return err
}

// execStream runs "stream start [title]" or "stream stop".
func (c mockCmd) execStream(ctx context.Context, e *Env, mp *mock.Platform, line string, fields []string) error {
	if len(fields) < 2 {
		return errors.New("the form is \"stream start [title]\" or \"stream stop\"")
	}
	switch fields[1] {
	case "start":
		title := strings.TrimSpace(strings.TrimPrefix(line, "stream start"))
		if err := mp.GoLive(ctx, title, ""); err != nil {
			return err
		}
		_, err := fmt.Fprintln(e.Stdout, "stream live")
		return err
	case "stop":
		if err := mp.GoOffline(ctx); err != nil {
			return err
		}
		_, err := fmt.Fprintln(e.Stdout, "stream offline")
		return err
	default:
		return errors.New("the form is \"stream start [title]\" or \"stream stop\"")
	}
}

// execUser runs "user add <login>".
func (c mockCmd) execUser(e *Env, mp *mock.Platform, fields []string) error {
	if len(fields) < 3 || fields[1] != "add" {
		return errors.New("the form is \"user add <login>\"")
	}
	ident, err := mp.AddUser(mock.UserSpec{Login: fields[2]})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.Stdout, "user %s added\n", ident.Login)
	return err
}

// importCommands loads the files of commands as code into the copy of the
// profile, with the checks of the import; with errors it imports nothing.
func (c mockCmd) importCommands(ctx context.Context, e *Env, cfg *config.Config, dir, dstPath string) (err error) {
	rights, err := cfg.Rights()
	if err != nil {
		return &usageError{err: err}
	}
	files, err := commandfile.Files(c.Commands)
	if err != nil {
		return &usageError{err: err}
	}
	if len(files) == 0 {
		return &usageError{err: fmt.Errorf("no files of commands as code in %s", strings.Join(c.Commands, ", "))}
	}
	l, err := app.LockDataDir(dir)
	if err != nil {
		return err
	}
	defer release(l, &err)
	report, err := app.ImportCommandFiles(ctx, dstPath, nil, rights, files)
	for _, p := range report.Problems {
		_, _ = fmt.Fprintln(e.Stdout, p.String())
	}
	if !report.Imported {
		return &reportedError{err: errors.New("the files have errors; nothing imported")}
	}
	_, err = fmt.Fprintf(e.Stdout, "imported into the copy: %d new, %d replaced\n", report.Created, report.Replaced)
	return err
}

// copyProfile copies the profile at src to dst as a backup snapshot does,
// without changing src.
func copyProfile(ctx context.Context, src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	ro, err := store.OpenReadOnly(ctx, src)
	if err != nil {
		return err
	}
	err = ro.VacuumInto(ctx, dst)
	return errors.Join(err, ro.Close())
}

// copyFile copies a small file, e.g. the vault key.
func copyFile(src, dst string) (err error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Clean(dst), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	_, err = f.Write(data)
	return err
}

// fileExists reports whether path is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
