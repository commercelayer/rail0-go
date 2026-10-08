package rail0

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// DisputeReason is a code from the gateway's dispute-reason dictionary, e.g.
// DisputeOpenReasonNotReceived ("not_received"). The prepare calls also accept a
// code's exact bytes32 as a DisputeReason("0x…"). The codes, their descriptions and
// bytes32 are generated into types_gen.go from the gateway's DisputeOpenReason,
// DisputeCloseReason and DisputeSystemCloseReason schemas.
type DisputeReason string

// DisputeReasonInfo is one dictionary entry. Bytes32 is what goes on-chain:
// keccak256("rail0.dispute.<code>") as lowercase 0x-hex.
type DisputeReasonInfo struct {
	Code        DisputeReason
	Description string
	Bytes32     Bytes32
}

// LookupDisputeOpenReason resolves a code name or its exact bytes32 (any hex case)
// against the reasons a dispute can be OPENED with — the same rule the gateway
// applies before answering 422 unknown_dispute_reason. Surrounding whitespace is
// ignored, as the gateway does.
func LookupDisputeOpenReason(reason string) (DisputeReasonInfo, bool) {
	return lookupDisputeReason(DisputeOpenReasons, reason)
}

// LookupDisputeCloseReason is LookupDisputeOpenReason for CLOSING a dispute. Only
// the caller-selectable reasons match: the system reason full_refund is recorded
// by the protocol itself and the gateway refuses it on the close prepare.
func LookupDisputeCloseReason(reason string) (DisputeReasonInfo, bool) {
	return lookupDisputeReason(DisputeCloseReasons, reason)
}

// lookupDisputeReason matches the code exactly, or the bytes32 case-insensitively.
// The list must be passed because a code (`other`) can sit in both the open and the
// close list with different descriptions — the kind decides which one applies.
func lookupDisputeReason(list []DisputeReasonInfo, reason string) (DisputeReasonInfo, bool) {
	v := strings.TrimSpace(reason)
	if v == "" {
		return DisputeReasonInfo{}, false
	}
	for _, e := range list {
		if string(e.Code) == v || strings.EqualFold(e.Bytes32, v) {
			return e, true
		}
	}
	return DisputeReasonInfo{}, false
}

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
