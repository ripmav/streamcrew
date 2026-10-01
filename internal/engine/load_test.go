// SPDX-License-Identifier: Apache-2.0

package engine_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/settings"
)

// TestLoad starts many commands at the same time in each lock mode (spec
// command-engine.md, acceptance criteria): all of them run, and no two
// instances that need the same lock run side by side. Run it with -race.
func TestLoad(t *testing.T) {
	t.Parallel()
	const (
		starters  = 50
		perStarts = 10
	)
	// exclusive returns the lock an instance of cmd holds under mode; empty
	// for none.
	exclusive := map[settings.LockMode]func(cmd command.Command) string{
		settings.LockPerCommandType: func(cmd command.Command) string { return string(cmd.Kind) },
		settings.LockPerActionType:  func(cmd command.Command) string { return cmd.Actions[0].DocType() },
		settings.LockVisualAudio: func(cmd command.Command) string {
			if cmd.Actions[0].DocType() == "sound" {
				return "visual_audio"
			}
			return ""
		},
		settings.LockSingular: func(command.Command) string { return "singular" },
		settings.LockNone:     func(command.Command) string { return "" },
	}
	for mode, lockOf := range exclusive {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixtureWithTypes(t, mode, visual("sound"))
				defer f.stop()

				var (
					mu    sync.Mutex
					inUse = make(map[string]int)
					peak  = make(map[string]int)
					ran   atomic.Int64
				)
				work := func(typ string) action {
					return action{typ: typ, fn: func(_ context.Context, run *engine.Run) error {
						lock := lockOf(run.Command())
						if lock != "" {
							mu.Lock()
							inUse[lock]++
							peak[lock] = max(peak[lock], inUse[lock])
							mu.Unlock()
							defer func() {
								mu.Lock()
								inUse[lock]--
								mu.Unlock()
							}()
						}
						time.Sleep(time.Millisecond)
						ran.Add(1)
						return nil
					}}
				}
				kinds := []command.Kind{command.KindChat, command.KindEvent, command.KindTimer}
				types := []string{"chat", "sound", "counter"}
				var cmds []command.Command
				for _, kind := range kinds {
					for _, typ := range types {
						cmds = append(cmds, f.command(string(kind)+" "+typ, kind, work(typ)))
					}
				}

				var wg sync.WaitGroup
				for s := range starters {
					wg.Go(func() {
						for i := range perStarts {
							_, err := f.engine.Start(t.Context(), cmds[(s+i)%len(cmds)], engine.Params{})
							assert.NoError(t, err)
						}
					})
				}
				wg.Wait()
				time.Sleep(time.Hour)
				synctest.Wait()

				require.Equal(t, int64(starters*perStarts), ran.Load())
				mu.Lock()
				defer mu.Unlock()
				for lock, n := range peak {
					assert.Equal(t, 1, n, "instances with lock %q at once", lock)
				}
			})
		})
	}
}
