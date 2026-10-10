// SPDX-License-Identifier: MIT

package httpclient

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
)

// maxPages is the safety cap of EachPage per call (Code-ADR-0014, point 7).
const maxPages = 200

// defaultPageSize is the page size of EachPage for a zero first
// (Code-ADR-0014, point 7).
const defaultPageSize = 100

// EachPage walks the pages of a cursor-paginated endpoint
// (Code-ADR-0014, point 7): it calls DoJSON with first (standard 100) as
// the page size and the after cursor, hands each page's data and the next
// cursor to fn, and stops on an empty cursor or an error from fn. The
// safety cap is 200 pages per call.
func (c *Client) EachPage[T any](ctx context.Context, req *http.Request, first int,
	fn func(items []T, nextCursor string) error) error {
	if first <= 0 {
		first = defaultPageSize
	}
	cursor := ""
	for range maxPages {
		r := req.Clone(ctx)
		q := r.URL.Query()
		q.Set("first", strconv.Itoa(first))
		if cursor != "" {
			q.Set("after", cursor)
		}
		r.URL.RawQuery = q.Encode()
		var envelope struct {
			Data       []T `json:"data"`
			Pagination struct {
				Cursor string `json:"cursor"`
			} `json:"pagination"`
		}
		if err := c.DoJSON(ctx, r, &envelope); err != nil {
			return err
		}
		if err := fn(envelope.Data, envelope.Pagination.Cursor); err != nil {
			return err
		}
		if envelope.Pagination.Cursor == "" {
			return nil
		}
		cursor = envelope.Pagination.Cursor
	}
	return fmt.Errorf("httpclient: EachPage: exceeded %d pages", maxPages)
}
