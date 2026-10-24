// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
)

// Test helpers for the Bubble Tea v2 test harness (teatest/v2). The module
// lives under x/exp: the API may break, so only the narrow API of
// NewTestModel/Type/Send/Quit/WaitFor/FinalModel/FinalOutput is used.

// newTestModel drives the TUI model through the real Bubble Tea program
// loop of the teatest harness, with the renderer on so that the rendered
// output is asserted.
func newTestModel(t *testing.T, opts Options) *teatest.TestModel {
	t.Helper()
	return teatest.NewTestModel(t, newModel(opts), teatest.WithInitialTermSize(120, 40))
}

// newTestModelWithCore drives the model against an in-process mock core and
// returns the test model and the base URL for direct API access.
func newTestModelWithCore(t *testing.T) (*teatest.TestModel, string) {
	t.Helper()
	base := startMockCore(t)
	return newTestModel(t, Options{Server: base, Token: testToken}), base
}

// quitModel ends the program of the test and returns its final model.
func quitModel(t *testing.T, tm *teatest.TestModel) model {
	t.Helper()
	_ = tm.Quit()
	tm.WaitFinished(t, teatest.WithFinalTimeout(10*time.Second))
	return tm.FinalModel(t).(model)
}

// ansiEscapeRE matches ANSI escape sequences (CSI sequences and the
// other two-byte escapes) in the rendered output.
var ansiEscapeRE = regexp.MustCompile(`\x1b(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])`)

// waitOutput waits until the rendered output of the program contains want.
// The comparison is on the escape-stripped text: the diff renderer may
// rewrite a line in several chunks (cursor moves and erase sequences can
// land inside the expected phrase), so matching raw bytes flakes under
// load.
func waitOutput(t *testing.T, tm *teatest.TestModel, want string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(ansiEscapeRE.ReplaceAllString(string(b), ""), want)
	}, teatest.WithDuration(10*time.Second))
}

// commandClient returns the command service client of the core at base.
func commandClient(t *testing.T, base string) streamcrewv1alpha1connect.CommandServiceClient {
	t.Helper()
	client, base := httpClientFor(base)
	return streamcrewv1alpha1connect.NewCommandServiceClient(connect.NewClient(
		connecthttp.NewTransport(client, base, connecthttp.WithReadMaxBytes(0)), bearerInterceptor(testToken)))
}

// streamClient returns the stream service client of the core at base.
func streamClient(t *testing.T, base string) streamcrewv1alpha1connect.StreamServiceClient {
	t.Helper()
	client, base := httpClientFor(base)
	return streamcrewv1alpha1connect.NewStreamServiceClient(connect.NewClient(
		connecthttp.NewTransport(client, base, connecthttp.WithReadMaxBytes(0)), bearerInterceptor(testToken)))
}

// commandNames returns the command names of the list (for assertions).
func commandNames(list []*v1alpha1.CommandSummary) []string {
	names := make([]string, len(list))
	for i, c := range list {
		names[i] = c.GetName()
	}
	return names
}

// subscribeStream subscribes to the live stream of the core at base.
func subscribeStream(t *testing.T, base string) streamcrewv1alpha1connect.StreamServiceSubscribeClientStream {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	sub, err := streamClient(t, base).Subscribe(ctx, &v1alpha1.SubscribeRequest{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() })
	return sub
}

// waitForLogEvent waits until a log event containing want arrives on the
// stream. The mock platform logs every operation it receives ("mock
// output", INFO) and the core re-emits its log records as stream events, so
// an event is proof that the TUI's API call reached the core.
func waitForLogEvent(t *testing.T, sub streamcrewv1alpha1connect.StreamServiceSubscribeClientStream, want string) {
	t.Helper()
	for {
		ev, err := sub.Receive()
		if err != nil {
			t.Fatalf("waiting for a log event containing %q: %v", want, err)
		}
		// the stream carries the protocol type (see convertEvent); a log
		// event is the proof that the TUI's API call reached the core
		if ev.GetType() == "log" {
			if l := ev.GetLog(); l != nil && strings.Contains(l.GetMessage(), want) {
				return
			}
		}
	}
}
