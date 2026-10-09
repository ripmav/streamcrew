// SPDX-License-Identifier: Apache-2.0

package httpclient_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEachPageWalksAllPages(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		var queries []string
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			queries = append(queries, r.URL.RawQuery)
			switch r.URL.Query().Get("after") {
			case "":
				_, _ = w.Write([]byte(`{"data":[1,2],"pagination":{"cursor":"a"}}`))
			case "a":
				_, _ = w.Write([]byte(`{"data":[3],"pagination":{"cursor":"b"}}`))
			case "b":
				_, _ = w.Write([]byte(`{"data":[],"pagination":{"cursor":""}}`))
			default:
				w.WriteHeader(http.StatusInternalServerError)
			}
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), nil)
		req := newGetRequest(t.Context(), srv.URL+"/helix/users")
		var all []int
		err := c.EachPage[int](t.Context(), req, 0, func(page []int, _ string) error {
			all = append(all, page...)
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2, 3}, all)
		assert.Equal(t, int64(3), calls.Load(), "three pages, the empty cursor ends the walk")
		assert.Contains(t, queries[0], "first=100", "the standard page size")
		assert.Contains(t, queries[1], "after=a")
		assert.Contains(t, queries[2], "after=b")
	})
}

func TestEachPageAbortsOnFnError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.URL.Query().Get("first") != "10" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			switch r.URL.Query().Get("after") {
			case "":
				_, _ = w.Write([]byte(`{"data":[1],"pagination":{"cursor":"a"}}`))
			default:
				_, _ = w.Write([]byte(`{"data":[2],"pagination":{"cursor":"b"}}`))
			}
		}))
		defer srv.Close()
		c := newClient(t, srv.Client(), newBreaker(), nil)
		req := newGetRequest(t.Context(), srv.URL+"/helix/follows")
		wantErr := errors.New("stop walking")
		var pages int
		err := c.EachPage[int](t.Context(), req, 10, func(_ []int, _ string) error {
			pages++
			if pages == 2 {
				return wantErr
			}
			return nil
		})
		assert.ErrorIs(t, err, wantErr)
		assert.Equal(t, 2, pages)
		assert.Equal(t, int64(2), calls.Load(), "the walk stops after the error")
	})
}
