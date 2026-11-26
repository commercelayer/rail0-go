package rail0

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestAdminOperatorsList(t *testing.T) {
	s := newAdminServer(t, 200, `[
		{"id":"g2","account_id":"acc2","granted_by":"acc1","note":"on call","expires_at":"2026-12-01T00:00:00Z",
		 "created_at":"2026-11-26T10:00:00Z","account":{"id":"acc2","name":"Bea"},"grantor":{"id":"acc1","name":"Ada"}},
		{"id":"g1","account_id":"acc1","granted_by":null,"note":null,"expires_at":null,"created_at":"2026-08-24T17:07:11Z",
		 "account":{"id":"acc1","name":"Ada"},"grantor":null}]`)
	grants, err := s.client().Admin.Operators(context.Background())
	if err != nil {
		t.Fatalf("Operators: %v", err)
	}
	s.wantRequest(t, http.MethodGet, "/admin/administrators", nil)
	if len(grants) != 2 || grants[0].AccountID != "acc2" || grants[0].GrantedBy != "acc1" || grants[0].ExpiresAt == "" {
		t.Errorf("grants = %+v", grants)
	}
	// A grant made out of band has no grantor and no expiry.
	if g := grants[1]; g.GrantedBy != "" || g.Grantor != nil || g.ExpiresAt != "" || g.Note != "" {
		t.Errorf("bootstrap grant = %+v", g)
	}
}

func TestAdminGrantAndRevokeOperator(t *testing.T) {
	ctx := context.Background()

	s := newAdminServer(t, 201, `{"id":"g2","account_id":"acc2","granted_by":"acc1","note":"on call","expires_at":"2026-12-01T00:00:00Z"}`)
	g, err := s.client().Admin.GrantOperator(ctx, "acc2", GrantOperatorRequest{Note: "on call", ExpiresAt: "2026-12-01T00:00:00Z"})
	if err != nil || g.ID != "g2" || g.GrantedBy != "acc1" {
		t.Errorf("GrantOperator = %+v, %v", g, err)
	}
	s.wantRequest(t, http.MethodPost, "/admin/administrators/acc2", nil)
	var body map[string]any
	if err := json.Unmarshal(s.body, &body); err != nil || body["note"] != "on call" || body["expires_at"] != "2026-12-01T00:00:00Z" {
		t.Errorf("body = %s", s.body)
	}

	// With nothing to say, nothing is sent: no note, no expiry.
	s = newAdminServer(t, 201, `{"id":"g3","account_id":"acc3"}`)
	if _, err := s.client().Admin.GrantOperator(ctx, "acc3", GrantOperatorRequest{}); err != nil {
		t.Fatal(err)
	}
	var empty map[string]any
	if err := json.Unmarshal(s.body, &empty); err != nil || len(empty) != 0 {
		t.Errorf("an empty grant sent %s", s.body)
	}

	s = newAdminServer(t, 204, ``)
	if err := s.client().Admin.RevokeOperator(ctx, "acc2"); err != nil {
		t.Errorf("RevokeOperator: %v", err)
	}
	s.wantRequest(t, http.MethodDelete, "/admin/administrators/acc2", nil)

	// The refusals that protect the role each have a code and a hint of their own.
	for _, code := range []string{"self_revoke", "last_admin"} {
		s = newAdminServer(t, 422, `{"code":"`+code+`","title":"…","detail":"…"}`)
		err := s.client().Admin.RevokeOperator(ctx, "acc1")
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Code != code || apiErr.Hint() == "" {
			t.Errorf("%s: err = %v, hint %q", code, err, DescribeError(code))
		}
	}
	s = newAdminServer(t, 422, `{"code":"expiry_in_the_past","title":"…","detail":"…"}`)
	_, err = s.client().Admin.GrantOperator(ctx, "acc2", GrantOperatorRequest{ExpiresAt: "2020-01-01T00:00:00Z"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "expiry_in_the_past" || apiErr.Hint() == "" {
		t.Errorf("expiry_in_the_past: err = %v", err)
	}
}
