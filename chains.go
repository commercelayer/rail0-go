package rail0

import "context"

// ChainsService exposes the supported blockchain catalog (public, no auth).
type ChainsService struct {
	http *httpClient
}

// Blockchain is a supported EVM chain as returned by GET /blockchains.
type Blockchain struct {
	ChainID      int    `json:"chain_id"`
	Name         string `json:"name"`
	NativeSymbol string `json:"native_symbol"`
	NetworkType  string `json:"network_type"`
	ExplorerURL  string `json:"explorer_url"`
}

// List returns all active blockchains.
func (s *ChainsService) List(ctx context.Context) ([]Blockchain, error) {
	var out []Blockchain
	if err := s.http.get(ctx, "/blockchains", &out); err != nil {
		return nil, err
	}
	return out, nil
}
