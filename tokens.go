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
	// Active filters on the token's active flag. nil (the zero value) applies NO
	// filter and returns every token, including retired ones — a payment keeps
	// its token address forever, so resolving a historical payment needs them.
	// Set it to true on a path that must only offer what can still be transacted.
	Active *bool
}

// List returns the token catalog, optionally filtered by chain, symbol and/or
// active flag. Unfiltered it includes RETIRED tokens (Token.Active reports
// which); pass Active=true for only those usable for a new payment.
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
		if p.Active != nil {
			q.Set("active", strconv.FormatBool(*p.Active))
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
