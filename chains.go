package rail0

import (
	"context"
	"net/url"
)

// ChainsService exposes the supported blockchain catalog (public, no auth).
type ChainsService struct {
	http *httpClient
}

// Blockchain (the GET /blockchains response shape) is generated from the
// gateway OpenAPI schema in types_gen.go.
//
// Blockchain.Settlement (BlockchainSettlement, gateway #365) is the settlement
// time this gateway has measured on the chain: p50/p90 of broadcast
// (submitted_at) to confirmation (confirmed_at) over the confirmed transactions
// of the trailing WindowDays. It is end-to-end — the chain's finality lag plus
// the gateway/indexer pipeline — so it is what a caller polling a payment
// actually waits for. The object is always present; P50Seconds and P90Seconds
// are nil while SampleSize is below the gateway's minimum sample (20), which is
// why they are pointers: nil means "not measured yet", never "instant".
//
// Use it as a wait-deadline hint, never a guarantee — a small multiple of the
// p90, with a fixed fallback when it is nil:
//
//	deadline := 15 * time.Minute // fallback
//	if p90 := chain.Settlement.P90Seconds; p90 != nil {
//	    deadline = max(3*time.Duration(*p90)*time.Second, time.Minute)
//	}
//
// It is a long-window capacity figure, deliberately not a live "confirms are
// slow right now" signal.

// ListChainsParams holds optional query parameters for ChainsService.List.
type ListChainsParams struct {
	NetworkType string // filter by network type ("testnet" or "mainnet")
	Symbol      string // filter by native symbol (case-insensitive, e.g. "ETH", "USDC")
}

// List returns active blockchains, optionally filtered by network type and/or
// native symbol.
func (s *ChainsService) List(ctx context.Context, params ...ListChainsParams) ([]Blockchain, error) {
	path := "/blockchains"
	if len(params) > 0 {
		p := params[0]
		q := url.Values{}
		if p.NetworkType != "" {
			q.Set("network_type", p.NetworkType)
		}
		if p.Symbol != "" {
			q.Set("symbol", p.Symbol)
		}
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
	}
	var out []Blockchain
	if err := s.http.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}
