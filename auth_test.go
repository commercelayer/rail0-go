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
