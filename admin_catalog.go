package rail0

import "context"

// What the gateway is configured with and what it is configured to serve, as an operator
// reads it: the effective configuration of the running process, the chain catalogue with
// what is not in service, and the catalogue's in-memory snapshot against the database.
//
// These answers are returned as the gateway sends them (maps), not as typed records: they
// are "every column" views and a table of settings, both of which gain entries with a
// gateway release, and an operator reading them wants what the gateway said, whole.

// Config is the effective operator configuration of the process that answers
// (GET /admin/config): every window, threshold, limit and feature flag, grouped (build,
// payments, recovery, retention, webhooks, payouts, settlement, rate_limit, siwe, session,
// fees, rpc, onboarding, email), plus `generated_at`.
//
// Each setting is {value, env, overridden, description}: the value in use, the
// environment variable that sets it (null for a constant), whether that variable is set
// in this process (so a default and an override that happen to coincide read
// differently), and what the setting is for. ConfigSetting is that shape. No secret is
// in it. Read-only by nature: the values come from the environment at boot.
func (s *AdminService) Config(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := s.http.get(ctx, "/admin/config", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Blockchains lists every blockchain configured in this environment, the ones not in
// service included (GET /admin/blockchains): the whole row, with `active` and the
// gateway's own `rpc_urls`. The public Chains listing shows only payment targets.
//
// A keyed RPC endpoint appears as its template, with a {RAIL0_RPC_KEY_<NAME>}
// placeholder: the key lives in the gateway's environment and is not in this answer.
func (s *AdminService) Blockchains(ctx context.Context) ([]map[string]any, error) {
	return s.catalogRows(ctx, "/admin/blockchains")
}

// Tokens lists every token configured in this environment, those on chains that are not
// in service included (GET /admin/tokens): the whole row, with the EIP-712 domain the
// gateway signs against. It carries `blockchain_id` (the row's own reference), which
// Blockchains resolves to a chain.
func (s *AdminService) Tokens(ctx context.Context) ([]map[string]any, error) {
	return s.catalogRows(ctx, "/admin/tokens")
}

// Contracts lists every non-archived RAIL0 deployment known to this environment
// (GET /admin/contracts). Read with Blockchains to see what a chain still needs before it
// can serve payments.
func (s *AdminService) Contracts(ctx context.Context) ([]map[string]any, error) {
	return s.catalogRows(ctx, "/admin/contracts")
}

func (s *AdminService) catalogRows(ctx context.Context, path string) ([]map[string]any, error) {
	var out []map[string]any
	if err := s.http.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CatalogDiff compares the chain catalogue this process serves from memory with the
// database (GET /admin/catalog): what a reload would change. Stale and Pending say
// whether and how much; Blockchains, Tokens and Contracts each hold `added`, `removed`
// and `changed` rows, a changed row with its fields as {from, to}.
//
// ONE PROCESS: the snapshot compared is the one of the process that answered. A worker,
// or another web machine, has a snapshot of its own that this cannot see.
func (s *AdminService) CatalogDiff(ctx context.Context) (*CatalogDiff, error) {
	var out CatalogDiff
	if err := s.http.get(ctx, "/admin/catalog", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CatalogReload is what ReloadCatalog answers: what the rebuilt snapshot holds, and what
// the reload applied (the diff taken just before it).
type CatalogReload struct {
	Reloaded    bool `json:"reloaded"`
	Blockchains int  `json:"blockchains"`
	Tokens      int  `json:"tokens"`
	Contracts   int  `json:"contracts"`
	// PaymentBlockchains is how many chains can take a payment: active, with an active
	// token and a deployment.
	PaymentBlockchains int    `json:"payment_blockchains"`
	ReloadedAt         string `json:"reloaded_at,omitempty"`
	// Applied is the diff this reload applied: `pending` and the three lists of
	// CatalogDiff. `pending` 0 means the snapshot already matched the database.
	Applied map[string]any `json:"applied,omitempty"`
}

// ReloadCatalog rebuilds the chain catalogue's in-memory snapshot from the database
// (POST /admin/catalog/reload), for a change made in the database since boot: a chain
// activated, a token retired, an RPC endpoint corrected. A write: it needs a write-mode
// token (see AdminService) and is audited with what it applied.
//
// ONE PROCESS, the one that answers. It is the tool for a single-process environment; it
// is not a fleet-wide invalidation, and where several processes serve (web machines, a
// worker) a restart is what reaches them all.
func (s *AdminService) ReloadCatalog(ctx context.Context) (*CatalogReload, error) {
	var out CatalogReload
	if err := s.http.post(ctx, "/admin/catalog/reload", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
