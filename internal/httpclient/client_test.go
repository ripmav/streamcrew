// SPDX-License-Identifier: Apache-2.0

package httpclient_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/breaker"
	"github.com/ripmav/streamcrew/internal/httpclient"
)

// newClient builds a client for the name twitch.helix with the standard
// values and the given breaker. base is the transport of the test server's
// client (in-memory network, synctest-compatible); if nil, a plain client
// is used (only for tests where no request is sent).
func newClient(t *testing.T, base *http.Client, br *breaker.Breaker, log *slog.Logger) *httpclient.Client {
	t.Helper()
	return httpclient.New(httpclient.Options{
		Name:    "twitch.helix",
		Base:    base,
		Breaker: br,
		Logger:  log,
	})
}

func newBreaker() *breaker.Breaker {
	return breaker.New("twitch.helix", nil, httpclient.Evaluator)
}

func newGetRequest(ctx context.Context, u string) *http.Request {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		panic(err)
	}
	return req
}

func mustParseURL(u string) *url.URL {
	parsed, err := url.Parse(u)
	if err != nil {
		panic(err)
	}
	return parsed
}

func TestDoReturnsResponseWithRateLimit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		reset := time.Now().Add(5 * time.Second).Unix()
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.Header().Set("x-ratelimit-remaining", "29")
			w.Header().Set("x-ratelimit-reset", strconv.FormatInt(reset, 10))
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), nil)
		resp, err := c.Do(t.Context(), newGetRequest(t.Context(), srv.URL))
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, `{"ok":true}`, string(body))
		assert.Equal(t, 29, resp.RateLimit.Remaining)
		assert.Equal(t, time.Unix(reset, 0), resp.RateLimit.Reset)
		assert.Equal(t, int64(1), calls.Load())
	})
}

func TestDoRetriesServerErrors(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), nil)
		resp, err := c.Do(t.Context(), newGetRequest(t.Context(), srv.URL))
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, int64(2), calls.Load(), "the 500 is retried")
	})
}

func TestDoStopsOnClientErrors(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad request"}`))
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), nil)
		_, err := c.Do(t.Context(), newGetRequest(t.Context(), srv.URL))
		var se *httpclient.StatusError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, http.StatusBadRequest, se.StatusCode)
		assert.Contains(t, se.Snippet, "bad request")
		assert.Equal(t, int64(1), calls.Load(), "a 4xx is not retried")
	})
}

func TestDoRetriesNetworkErrors(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) == 1 {
				// Disconnect the connection without an answer: a network error.
				if h, ok := w.(http.Hijacker); ok {
					if conn, _, err := h.Hijack(); err == nil {
						_ = conn.Close()
					}
				}
				return
			}
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), nil)
		resp, err := c.Do(t.Context(), newGetRequest(t.Context(), srv.URL))
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, int64(2), calls.Load(), "the network error is retried")
	})
}

func TestDoBudgetExhausted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), nil)
		_, err := c.Do(t.Context(), newGetRequest(t.Context(), srv.URL))
		var se *httpclient.StatusError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, http.StatusInternalServerError, se.StatusCode)
		assert.Equal(t, int64(3), calls.Load(), "three attempts, then the last typed error")
	})
}

func TestDo429WaitsUntilReset(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		reset := time.Now().Add(2 * time.Second).Unix()
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) == 1 {
				w.Header().Set("x-ratelimit-reset", strconv.FormatInt(reset, 10))
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), nil)
		resp, err := c.Do(t.Context(), newGetRequest(t.Context(), srv.URL))
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, int64(2), calls.Load())
		assert.False(t, time.Now().Before(time.Unix(reset, 0)), "the retry waited until the reset")
	})
}

func TestDo429BeyondBudget(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		reset := time.Now().Add(40 * time.Second).Unix()
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.Header().Set("x-ratelimit-remaining", "0")
			w.Header().Set("x-ratelimit-reset", strconv.FormatInt(reset, 10))
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), nil)
		_, err := c.Do(t.Context(), newGetRequest(t.Context(), srv.URL))
		assert.ErrorIs(t, err, httpclient.ErrTooManyRequests)
		var se *httpclient.StatusError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, time.Unix(reset, 0), se.RateLimit.Reset)
		assert.Equal(t, int64(2), calls.Load(), "the budget ends the retries")
	})
}

func TestDo429WaitsRetryAfter(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) == 1 {
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), nil)
		t0 := time.Now()
		resp, err := c.Do(t.Context(), newGetRequest(t.Context(), srv.URL))
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, int64(2), calls.Load())
		assert.GreaterOrEqual(t, time.Since(t0), 2*time.Second, "the retry waited the Retry-After time")
	})
}

func TestDoRemainingZeroDelaysNextRequest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		reset := time.Now().Add(5 * time.Second).Unix()
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.Header().Set("x-ratelimit-remaining", "0")
			w.Header().Set("x-ratelimit-reset", strconv.FormatInt(reset, 10))
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), nil)
		resp, err := c.Do(t.Context(), newGetRequest(t.Context(), srv.URL))
		require.NoError(t, err)
		assert.Equal(t, 0, resp.RateLimit.Remaining)
		require.NoError(t, resp.Body.Close())
		// The next request does not go out before the reset.
		resp2, err := c.Do(t.Context(), newGetRequest(t.Context(), srv.URL))
		require.NoError(t, err)
		require.NoError(t, resp2.Body.Close())
		assert.Equal(t, int64(2), calls.Load())
		assert.False(t, time.Now().Before(time.Unix(reset, 0)), "the second request waited until the reset")
	})
}

func TestDoOpenBreakerMakesNoRequest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		br := newBreaker()
		c := newClient(t, nil, br, nil)
		// Open the breaker with five consecutive failures.
		for range 5 {
			err := br.Execute(t.Context(), func(context.Context) error {
				return &httpclient.StatusError{StatusCode: http.StatusInternalServerError}
			})
			require.Error(t, err)
		}
		_, err := c.Do(t.Context(), newGetRequest(t.Context(), "http://example.com/helix/users"))
		assert.ErrorIs(t, err, breaker.ErrUnavailable)
	})
}

func TestDoWithoutRetry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), nil)
		ctx := httpclient.WithoutRetry(t.Context())
		_, err := c.Do(ctx, newGetRequest(ctx, srv.URL))
		var se *httpclient.StatusError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, http.StatusInternalServerError, se.StatusCode)
		assert.Equal(t, int64(1), calls.Load(), "WithoutRetry makes a single attempt")
	})
}

func TestDoCanceledDuringRetry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		started := make(chan struct{}, 1)
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			started <- struct{}{}
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), nil)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := c.Do(ctx, newGetRequest(ctx, srv.URL))
			done <- err
		}()
		<-started
		synctest.Wait()
		cancel()
		select {
		case err := <-done:
			assert.ErrorIs(t, err, context.Canceled)
		case <-time.After(time.Second):
			t.Fatal("Do did not return after the cancel")
		}
		assert.Equal(t, int64(1), calls.Load(), "the cancel stops the retries")
	})
}

func TestDoCallerDeadlineAbortsRequest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			<-r.Context().Done()
			// The answer is not used; the client gave up.
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), nil)
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		_, err := c.Do(ctx, newGetRequest(ctx, srv.URL))
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, int64(1), calls.Load(), "a caller deadline is not retried")
	})
}

func TestDoJSONDecodes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"id":42,"name":"streamer"}`))
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), nil)
		var out struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}
		require.NoError(t, c.DoJSON(t.Context(), newGetRequest(t.Context(), srv.URL), &out))
		assert.Equal(t, 42, out.ID)
		assert.Equal(t, "streamer", out.Name)
	})
}

func TestDoJSONCarriesStatusError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"User not found"}`))
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), nil)
		var out any
		err := c.DoJSON(t.Context(), newGetRequest(t.Context(), srv.URL), &out)
		var se *httpclient.StatusError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, http.StatusNotFound, se.StatusCode)
		assert.Contains(t, se.Snippet, "User not found")
	})
}

func TestDoRejectsNonReplayableBody(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := newClient(t, nil, newBreaker(), nil)
		req := &http.Request{
			Method: http.MethodPost,
			URL:    mustParseURL("https://api.twitch.tv/helix/chat/messages"),
			Body:   io.NopCloser(strings.NewReader(`{"x":1}`)),
		}
		_, err := c.Do(t.Context(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "replayable")
	})
}

func TestDoLogsRetries(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		log := slog.New(slog.NewTextHandler(&buf, nil))
		var calls atomic.Int64
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), log)
		_, err := c.Do(t.Context(), newGetRequest(t.Context(), srv.URL))
		require.Error(t, err)
		assert.Equal(t, int64(3), calls.Load())
		assert.Equal(t, 2, strings.Count(buf.String(), `msg="retrying request"`), "a WARN per retry")
	})
}

func TestDoLogsRateLimitAsInfo(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		log := slog.New(slog.NewTextHandler(&buf, nil))
		var calls atomic.Int64
		reset := time.Now().Add(2 * time.Second).Unix()
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) == 1 {
				w.Header().Set("x-ratelimit-reset", strconv.FormatInt(reset, 10))
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), log)
		resp, err := c.Do(t.Context(), newGetRequest(t.Context(), srv.URL))
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Contains(t, buf.String(), `msg="rate limited"`, "the 429 wait is an INFO")
		assert.NotContains(t, buf.String(), `msg="retrying request"`)
	})
}

func TestEvaluator(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want breaker.Eval
	}{
		{"nil", nil, breaker.EvalSuccess},
		{"status 500", &httpclient.StatusError{StatusCode: http.StatusInternalServerError}, breaker.EvalError},
		{"status 404", &httpclient.StatusError{StatusCode: http.StatusNotFound}, breaker.EvalSuccess},
		{"status 429", &httpclient.StatusError{StatusCode: http.StatusTooManyRequests}, breaker.EvalExcluded},
		{"wrapped 500", fmt.Errorf("outer: %w", &httpclient.StatusError{StatusCode: http.StatusInternalServerError}), breaker.EvalError},
		{"canceled", context.Canceled, breaker.EvalExcluded},
		{"network", &url.Error{Op: "Get", URL: "https://api.twitch.tv/helix/users", Err: errors.New("connection refused")}, breaker.EvalError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, httpclient.Evaluator(tt.err))
		})
	}
}
