package rail0

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
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

// ResolveChain finds the chain ref names in chains (typically ChainsService.List's
// answer): a numeric ref is a chain id ("5042002"), anything else a slug ("arc"),
// compared case-insensitively. A slug is shared by a chain's testnet and mainnet, and a
// gateway serves one network class, so against one gateway's catalog it names one chain.
//
// It errors when ref is empty, when nothing matches (the error lists the available
// slugs), and when a slug matches more than one chain — a catalog mixing network
// classes, which a single gateway never returns: filter it by NetworkType first.
func ResolveChain(chains []Blockchain, ref string) (*Blockchain, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("rail0: no chain given: pass a chain id or a slug")
	}
	if id, err := strconv.Atoi(ref); err == nil {
		for i := range chains {
			if chains[i].ChainID == id {
				return &chains[i], nil
			}
		}
		return nil, fmt.Errorf("rail0: no chain with id %d (available: %s)", id, availableChains(chains))
	}
	var found []*Blockchain
	for i := range chains {
		if chains[i].Slug != "" && strings.EqualFold(chains[i].Slug, ref) {
			found = append(found, &chains[i])
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return nil, fmt.Errorf("rail0: no chain with slug %q (available: %s)", ref, availableChains(chains))
	default:
		ids := make([]string, len(found))
		for i, c := range found {
			ids[i] = fmt.Sprintf("%d (%s)", c.ChainID, c.NetworkType)
		}
		return nil, fmt.Errorf("rail0: slug %q matches %d chains: %s — pass the chain id instead", ref, len(found), strings.Join(ids, ", "))
	}
}

// availableChains lists a catalog for an error message: the sorted slugs, each with its
// chain id ("arc (5042002), base (84532)"), or "none" for an empty catalog.
func availableChains(chains []Blockchain) string {
	if len(chains) == 0 {
		return "none"
	}
	out := make([]string, 0, len(chains))
	for _, c := range chains {
		name := c.Slug
		if name == "" {
			name = c.Name
		}
		out = append(out, fmt.Sprintf("%s (%d)", name, c.ChainID))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
