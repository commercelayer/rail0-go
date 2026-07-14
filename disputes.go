package rail0

import (
	"context"
	"net/url"
	"strconv"
)

// DisputesService lists disputes across the authenticated account's payments
// (requires JWT). Unlike PaymentsService.Disputes — which returns one payment's
// open/close history — this surfaces every dispute, open AND closed, on the
// caller's payments (as payer or payee) in one call, each with its parent
// payment embedded. A closed dispute drops out of the `disputed` flag on
// Payments.List (current-state) but still appears here.
type DisputesService struct {
	http *httpClient
}

// List returns the account's disputes. ListDisputesParams (shared with
// Payments.Disputes) filters by Status ("open"/"closed") and Sort/Page/PerPage.
// The gateway returns a bare JSON array with pagination in response headers.
func (s *DisputesService) List(ctx context.Context, params ...ListDisputesParams) (*PaginatedResponse[DisputeDetail], error) {
	path := "/disputes"
	if len(params) > 0 {
		p := params[0]
		q := url.Values{}
		if p.Status != "" {
			q.Set("status", p.Status)
		}
		if p.Sort != "" {
			q.Set("sort", p.Sort)
		}
		if p.Page > 0 {
			q.Set("page", strconv.Itoa(p.Page))
		}
		if p.PerPage > 0 {
			q.Set("per_page", strconv.Itoa(p.PerPage))
		}
		if len(q) > 0 {
			path = path + "?" + q.Encode()
		}
	}
	var data []DisputeDetail
	hdr, err := s.http.getList(ctx, path, &data)
	if err != nil {
		return nil, err
	}
	return &PaginatedResponse[DisputeDetail]{Data: data, Meta: pageMetaFromHeaders(hdr)}, nil
}
