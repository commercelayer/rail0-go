package rail0

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// Onboarding requests, as the operator handles them: the list, an invite, a correction, a
// resend and a cancel. A request becomes an account only when the merchant claims it, by
// signing with the wallet the request names, from the link in the email.
//
// NO ANSWER CARRIES THE LINK. It exists only in the email the gateway sends, so nothing
// here can hand an operator a credential for someone else's account. Every write that
// sends a new link makes the previous one stop working.
//
// Every write is audited on the gateway and needs a write-mode token (see AdminService).

// OnboardingListed is a row of the operator's list: the request and its clashes with
// accounts and with other requests not yet completed.
type OnboardingListed struct {
	Onboarding
	Conflicts []OnboardingConflict `json:"conflicts"`
}

// ListOnboardingsParams filters GET /admin/onboardings.
type ListOnboardingsParams struct {
	// Statuses: any of "open", "expired", "completed". Empty means the gateway's default,
	// open and expired: the requests still waiting for something.
	Statuses []string
	// Origin: "operator" (an invite) or "signup" (self-service).
	Origin string
	// SendFailed keeps only the requests whose last send failed (true) or only the others
	// (false); nil does not filter.
	SendFailed *bool
	Sort       string // created_at, expires_at, last_sent_at, name or email; prefix with - for desc
	Page       int
	PerPage    int
}

// Onboardings lists onboarding requests, newest first.
func (s *AdminService) Onboardings(ctx context.Context, params ...ListOnboardingsParams) (*PaginatedResponse[OnboardingListed], error) {
	q := url.Values{}
	if len(params) > 0 {
		p := params[0]
		setNonEmpty(q, "status", strings.Join(p.Statuses, ","))
		setNonEmpty(q, "origin", p.Origin)
		if p.SendFailed != nil {
			q.Set("send_failed", strconv.FormatBool(*p.SendFailed))
		}
		setPaging(q, p.Sort, p.Page, p.PerPage)
	}
	return listPage[OnboardingListed](ctx, s.http, withQuery("/admin/onboardings", q))
}

// InviteRequest is the merchant an operator invites: the account's name, the contact
// email the link goes to, and the wallet address that must sign the claim.
type InviteRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Address string `json:"address"`
}

// ErrInviteNeedsSession is returned by Invite on a client that carries no token.
var ErrInviteNeedsSession = errors.New("rail0: Invite needs an operator session: without a token the same request is a public sign-up")

// Invite creates an onboarding request for a merchant and emails them the link
// (POST /signup with the operator's session). Repeating it for an open invite with the
// same email and address sends a new link for that request instead of creating a second.
//
// The route is the public sign-up's: what makes the request an invite is the token. So a
// client without one is refused here (ErrInviteNeedsSession) and nothing is sent, where
// the gateway would have taken it as a self-service sign-up.
//
// A name, email or address an account already holds is refused with 409
// (`account_name_taken`, `account_email_taken`, `account_wallet_taken`); an email outside
// the domains the environment may write to with 422 `email_domain_not_allowed`.
func (s *AdminService) Invite(ctx context.Context, req InviteRequest) (*OnboardingOutcome, error) {
	if s.http.getToken() == "" {
		return nil, ErrInviteNeedsSession
	}
	var out OnboardingOutcome
	if err := s.http.post(ctx, "/signup", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CorrectOnboardingRequest is the body of CorrectOnboarding: the values to replace. At
// least one must be set, and at least one must differ from the request's.
type CorrectOnboardingRequest struct {
	Name    string `json:"name,omitempty"`
	Email   string `json:"email,omitempty"`
	Address string `json:"address,omitempty"`
}

// CorrectOnboarding replaces the name, email or address of a request that is not yet
// completed (an expired one is renewed) and sends a new link to its email, saying what
// changed. Nothing goes to the address it replaced. The same checks as an invite run on
// the resulting values; a completed request answers 422 `onboarding_completed`.
func (s *AdminService) CorrectOnboarding(ctx context.Context, id string, req CorrectOnboardingRequest) (*OnboardingOutcome, error) {
	var out OnboardingOutcome
	if err := s.http.patch(ctx, pathf("/admin/onboardings/%s", id), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ResendOnboarding sends a request's link again: the invite again, or a reminder for a
// sign-up. Also how a failed send is retried. It refuses with 409 when an account has
// taken the request's name, email or address since: correct the request first.
func (s *AdminService) ResendOnboarding(ctx context.Context, id string) (*OnboardingSent, error) {
	var out OnboardingSent
	if err := s.http.post(ctx, pathf("/admin/onboardings/%s/send", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CancelOnboarding deletes a request that is not completed: its link stops working. A
// completed request is history (422 `onboarding_completed`): to stop that merchant,
// deactivate the account.
func (s *AdminService) CancelOnboarding(ctx context.Context, id string) error {
	return s.http.delete(ctx, pathf("/admin/onboardings/%s", id), nil)
}
