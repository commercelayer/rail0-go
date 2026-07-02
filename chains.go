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
