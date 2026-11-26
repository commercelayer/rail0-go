package rail0

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestAdminOnboardingsList(t *testing.T) {
	ctx := context.Background()
	s := newAdminServer(t, 200, `[{"id":"ob1","name":"Acme","email":"ops@acme.example","address":"0x2d2b","origin":"operator",
		"status":"open","send_count":2,"last_send_error":"provider_rejected",
		"conflicts":[{"field":"email","with":"account","id":"acc9"}]}]`)
	yes := true
	page, err := s.client().Admin.Onboardings(ctx, ListOnboardingsParams{
		Statuses: []string{"open", "completed"}, Origin: "operator", SendFailed: &yes, Sort: "-last_sent_at", Page: 2, PerPage: 25,
	})
	if err != nil {
		t.Fatalf("Onboardings: %v", err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/onboardings", map[string]string{
		"status": "open,completed", "origin": "operator", "send_failed": "true", "sort": "-last_sent_at", "page": "2", "per_page": "25",
	})
	row := page.Data[0]
	if row.ID != "ob1" || row.LastSendError != "provider_rejected" || len(row.Conflicts) != 1 || row.Conflicts[0].ID != "acc9" {
		t.Errorf("row = %+v", row)
	}

	// No parameters: the gateway's own default (open and expired), nothing sent.
	s = newAdminServer(t, 200, `[]`)
	if _, err := s.client().Admin.Onboardings(ctx); err != nil {
		t.Fatal(err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/onboardings", nil)
}

func TestAdminInvite(t *testing.T) {
	ctx := context.Background()
	s := newAdminServer(t, 201, `{"onboarding":{"id":"ob1","origin":"operator","status":"open"},"conflicts":[],
		"email":{"kind":"invite","to":"ops@acme.example","queued":true}}`)
	out, err := s.client().Admin.Invite(ctx, InviteRequest{Name: "Acme", Email: "ops@acme.example", Address: "0x2d2b"})
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	s.wantRequest(t, http.MethodPost, "/signup", nil)
	if s.auth != "Bearer session-jwt" {
		t.Errorf("Authorization = %q: the invite must present the operator's token", s.auth)
	}
	var body map[string]string
	if err := json.Unmarshal(s.body, &body); err != nil || body["name"] != "Acme" || body["email"] != "ops@acme.example" || body["address"] != "0x2d2b" {
		t.Errorf("body = %s (%v)", s.body, err)
	}
	if out.Onboarding.ID != "ob1" || out.Email.Kind != "invite" || !out.Email.Queued {
		t.Errorf("outcome = %+v", out)
	}
}

// Without a token POST /signup is the PUBLIC sign-up. An invite from a client that
// carries none must not reach the gateway at all.
func TestAdminInviteWithoutATokenSendsNothing(t *testing.T) {
	s := newAdminServer(t, 202, `{"status":"check_your_email"}`)
	c := NewClient(ClientOptions{BaseURL: s.URL})
	_, err := c.Admin.Invite(context.Background(), InviteRequest{Name: "Acme", Email: "ops@acme.example", Address: "0x2d2b"})
	if !errors.Is(err, ErrInviteNeedsSession) {
		t.Errorf("err = %v, want ErrInviteNeedsSession", err)
	}
	if s.method != "" {
		t.Errorf("a request was sent: %s %s", s.method, s.path)
	}
}

func TestAdminOnboardingCorrectResendCancel(t *testing.T) {
	ctx := context.Background()

	s := newAdminServer(t, 200, `{"onboarding":{"id":"ob1","email":"new@acme.example","link_version":3},"conflicts":[],
		"email":{"kind":"correction","to":"new@acme.example","queued":true}}`)
	out, err := s.client().Admin.CorrectOnboarding(ctx, "ob1", CorrectOnboardingRequest{Email: "new@acme.example"})
	if err != nil || out.Onboarding.LinkVersion != 3 || out.Email.Kind != "correction" {
		t.Errorf("CorrectOnboarding = %+v, %v", out, err)
	}
	s.wantRequest(t, http.MethodPatch, "/admin/onboardings/ob1", nil)
	var body map[string]any
	if err := json.Unmarshal(s.body, &body); err != nil || len(body) != 1 || body["email"] != "new@acme.example" {
		t.Errorf("body = %s: only the corrected field goes out", s.body)
	}

	s = newAdminServer(t, 200, `{"onboarding":{"id":"ob1","send_count":4},"email":{"kind":"invite","to":"ops@acme.example","queued":true}}`)
	sent, err := s.client().Admin.ResendOnboarding(ctx, "ob1")
	if err != nil || sent.Onboarding.SendCount != 4 || sent.Email.To != "ops@acme.example" {
		t.Errorf("ResendOnboarding = %+v, %v", sent, err)
	}
	s.wantRequest(t, http.MethodPost, "/admin/onboardings/ob1/send", nil)

	s = newAdminServer(t, 204, ``)
	if err := s.client().Admin.CancelOnboarding(ctx, "ob1"); err != nil {
		t.Errorf("CancelOnboarding: %v", err)
	}
	s.wantRequest(t, http.MethodDelete, "/admin/onboardings/ob1", nil)

	s = newAdminServer(t, 422, `{"code":"onboarding_completed","title":"Onboarding already completed","detail":"…"}`)
	err = s.client().Admin.CancelOnboarding(ctx, "ob1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "onboarding_completed" || apiErr.Hint() == "" {
		t.Errorf("err = %v", err)
	}
}
