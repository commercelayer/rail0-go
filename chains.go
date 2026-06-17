package rail0

import "context"

// ChainsService exposes the supported blockchain catalog (public, no auth).
type ChainsService struct {
	http *httpClient
}

// Blockchain (the GET /blockchains response shape) is generated from the
// gateway OpenAPI schema in types_gen.go.

// List returns all active blockchains.
func (s *ChainsService) List(ctx context.Context) ([]Blockchain, error) {
	var out []Blockchain
	if err := s.http.get(ctx, "/blockchains", &out); err != nil {
		return nil, err
	}
	return out, nil
}
