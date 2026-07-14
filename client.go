// Package rail0 provides a Go client for the RAIL0 stablecoin payment API.
//
// Quick start:
//
//	client := rail0.NewClient(rail0.ClientOptions{BaseURL: "https://api.rail0.xyz"})
//	resp, err := client.Payments.CreatePayment(ctx, rail0.CreatePaymentRequest{...})
package rail0

// Client is the entry point for the RAIL0 SDK.
type Client struct {
	// Wallets exposes account wallet management under /accounts/:id/wallets (JWT).
	Wallets *WalletsService
	// PaymentMethods exposes the public GET /payment_methods discovery (no JWT).
	PaymentMethods *PaymentMethodsService
	// Auth exposes SIWE authentication: GetNonce, Verify, Login.
	Auth *AuthService
	// Chains exposes the public blockchain catalog.
	Chains *ChainsService
	// Tokens exposes the public token catalog.
	Tokens *TokensService
	// Payments exposes the full payment lifecycle.
	Payments *PaymentsService
	// Disputes exposes the account-level dispute list (open and closed).
	Disputes *DisputesService
	// Webhooks exposes webhook subscription management (JWT).
	Webhooks *WebhooksService
	// Health exposes the gateway liveness/readiness check.
	Health *HealthService
}

// NewClient creates a new Client with the provided options.
func NewClient(opts ClientOptions) *Client {
	h := newHTTPClient(opts)
	return &Client{
		Wallets:        &WalletsService{http: h},
		PaymentMethods: &PaymentMethodsService{http: h},
		Auth:           &AuthService{http: h},
		Chains:         &ChainsService{http: h},
		Tokens:         &TokensService{http: h},
		Payments:       &PaymentsService{http: h},
		Disputes:       &DisputesService{http: h},
		Webhooks:       &WebhooksService{http: h},
		Health:         &HealthService{http: h},
	}
}
