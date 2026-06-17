package rail0

import (
	"context"
	"strconv"
)

// TokensService exposes the supported token catalog (public, no auth).
type TokensService struct {
	http *httpClient
}

// Token (the GET /tokens response shape) is generated from the gateway OpenAPI
// schema in types_gen.go.

// List returns active tokens. Pass chainID > 0 to filter by chain (GET /tokens?chain_id=).
func (s *TokensService) List(ctx context.Context, chainID int) ([]Token, error) {
	var out []Token
	path := "/tokens"
	if chainID > 0 {
		path += "?chain_id=" + strconv.Itoa(chainID)
	}
	if err := s.http.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}
