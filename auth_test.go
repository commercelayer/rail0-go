package rail0

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Logout is the difference between "I stopped using this token" and "this token
// no longer works". Deleting a stored copy leaves the JWT valid until its `exp`,
// so whoever holds another copy keeps the session — hence the gateway's denylist,
// and hence these two properties: the call must carry the bearer token being
// revoked, and it must clear it locally afterwards.
func TestAuthLogoutRevokesAndClearsTheToken(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"revoked": true})
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt-abc"})

	res, err := c.Auth.Logout(context.Background())
	if err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if !res.Revoked {
		t.Error("Revoked = false, want the gateway's true")
	}
	if gotMethod != http.MethodPost || gotPath != "/auth/logout" {
		t.Errorf("called %s %s, want POST /auth/logout", gotMethod, gotPath)
	}
	if gotAuth != "Bearer jwt-abc" {
		t.Errorf("Authorization = %q, want the token being revoked", gotAuth)
	}

	// Cleared locally: continuing to send a credential we just asked to have
	// invalidated is how a "logged out" client keeps acting logged in.
	var sentAfter string
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sentAfter = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv2.Close()
	c2 := NewClient(ClientOptions{BaseURL: srv2.URL, Token: "jwt-abc"})
	if _, err := c2.Auth.Logout(context.Background()); err != nil {
		t.Fatalf("Logout on srv2: %v", err)
	}
	_, _ = c2.Health.Get(context.Background())
	if sentAfter != "" && sentAfter != "Bearer " {
		t.Errorf("still sending %q after logout", sentAfter)
	}
}

// `revoked: false` is a real answer, not an error: the gateway accepted the
// request but could not record the revocation, so the token stays usable until
// exp. A caller must be able to tell that apart from success.
func TestAuthLogoutSurfacesAFailedRevocation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"revoked": false})
	}))
	defer srv.Close()

	res, err := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt"}).Auth.Logout(context.Background())
	if err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if res.Revoked {
		t.Error("Revoked = true, want the gateway's false")
	}
}

// RevokeAll is the endpoint for a key you no longer trust, and the two things a wrong
// implementation gets wrong are both asserted here: it must carry the CUTOFF back (a
// boolean cannot say which sessions died) and it must clear the local token, exactly as
// Logout does — a client that keeps sending a credential it just had invalidated is a
// client that has not revoked anything from its own point of view.
func TestAuthRevokeAllReturnsTheCutoffAndClearsTheToken(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"revoked": true,
			"cutoff":  "2026-08-27T21:00:00Z",
		})
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt-abc"})

	res, err := c.Auth.RevokeAll(context.Background())
	if err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}
	if !res.Revoked {
		t.Error("Revoked = false, want the gateway's true")
	}
	if res.Cutoff != "2026-08-27T21:00:00Z" {
		t.Errorf("Cutoff = %q, want the instant the gateway recorded", res.Cutoff)
	}
	if gotMethod != http.MethodPost || gotPath != "/auth/revoke_all" {
		t.Errorf("called %s %s, want POST /auth/revoke_all", gotMethod, gotPath)
	}
	if gotAuth != "Bearer jwt-abc" {
		t.Errorf("Authorization = %q, want the session proving whose address this is", gotAuth)
	}

	var sentAfter string
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sentAfter = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv2.Close()
	c2 := NewClient(ClientOptions{BaseURL: srv2.URL, Token: "jwt-abc"})
	if _, err := c2.Auth.RevokeAll(context.Background()); err != nil {
		t.Fatalf("RevokeAll on srv2: %v", err)
	}
	_, _ = c2.Health.Get(context.Background())
	if sentAfter != "" && sentAfter != "Bearer " {
		t.Errorf("Authorization after RevokeAll = %q, want it cleared", sentAfter)
	}
}
