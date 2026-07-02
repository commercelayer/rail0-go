package rail0

import (
	"context"
	"net/url"
	"strconv"
)

// TokensService exposes the supported token catalog (public, no auth).
type TokensService struct {
	http *httpClient
}

// Token (the GET /tokens response shape) is generated from the gateway OpenAPI
// schema in types_gen.go.

// ListTokensParams holds optional query parameters for TokensService.List.
type ListTokensParams struct {
	ChainID int    // filter by chain id; 0 = no filter
	Symbol  string // filter by token symbol (case-insensitive, e.g. "USDC")
}

// List returns active tokens, optionally filtered by chain and/or symbol.
func (s *TokensService) List(ctx context.Context, params ...ListTokensParams) ([]Token, error) {
	path := "/tokens"
	if len(params) > 0 {
		p := params[0]
		q := url.Values{}
		if p.ChainID > 0 {
			q.Set("chain_id", strconv.Itoa(p.ChainID))
		}
		if p.Symbol != "" {
			q.Set("symbol", p.Symbol)
		}
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
	}
	var out []Token
	if err := s.http.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}
