// Package rail0 provides a Go client for the RAIL0 stablecoin payment API.
//
// Quick start:
//
//	client := rail0.NewClient(rail0.ClientOptions{BaseURL: "https://api.rail0.xyz"})
//	resp, err := client.Payments.CreatePayment(ctx, rail0.CreatePaymentRequest{...})
package rail0

// Client is the entry point for the RAIL0 SDK.
type Client struct {
	// http is the shared transport every service uses; held here so SetToken can
	// stash a JWT that then applies to all services at once.
	http *httpClient
	// Wallets exposes account wallet management under /accounts/:id/wallets (JWT).
	Accounts *AccountsService

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
	// Analytics exposes the account-scoped merchant sales analytics (JWT + account).
	Analytics *AnalyticsService
	// Health exposes the gateway liveness/readiness check.
	Health *HealthService
}

// NewClient creates a new Client with the provided options.
func NewClient(opts ClientOptions) *Client {
	h := newHTTPClient(opts)
	return &Client{
		http:           h,
		Accounts:       &AccountsService{http: h},
		Wallets:        &WalletsService{http: h},
		PaymentMethods: &PaymentMethodsService{http: h},
		Auth:           &AuthService{http: h},
		Chains:         &ChainsService{http: h},
		Tokens:         &TokensService{http: h},
		Payments:       &PaymentsService{http: h},
		Disputes:       &DisputesService{http: h},
		Webhooks:       &WebhooksService{http: h},
		Analytics:      &AnalyticsService{http: h},
		Health:         &HealthService{http: h},
	}
}

// SetToken stashes a JWT on the shared transport so every service sends
// "Authorization: Bearer <token>" on subsequent requests. Use it to authenticate a
// client built without a Token option — e.g. with the token returned by Auth.Verify
// (Auth.Login/Verify also do this for you). Pass "" to clear it. Safe to call
// concurrently with in-flight requests.
func (c *Client) SetToken(token string) { c.http.SetToken(token) }
