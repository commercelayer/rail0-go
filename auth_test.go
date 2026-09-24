package rail0

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// RevokeAll is the endpoint for a key you no longer trust. What a wrong implementation
// gets wrong is all asserted here: it must be authorized by a fresh SIWE proof carrying
// the REVOKE-ALL statement (an empty body is a 400, a login proof a 422), it must read
// the cutoff back under the gateway's own names, and it must clear the local token —
// the gateway's cutoff is self-revoking, so that token is dead anyway.
func TestAuthRevokeAllSignsAPurposeBoundProofAndClearsTheToken(t *testing.T) {
	var body AuthRequest
	var sentAfter string
	revoked := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/nonces":
			_ = json.NewEncoder(w).Encode(map[string]string{"nonce": "abc123nonce9", "expires_at": "2030-01-01T00:00:00Z"})
		case "/auth/revoke_all":
			_ = json.NewDecoder(r.Body).Decode(&body)
			revoked = true
			_ = json.NewEncoder(w).Encode(map[string]any{"revoked_all": true, "cutoff_at": "2026-08-27T21:00:00Z"})
		default:
			sentAfter = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt-abc"})
	key, _ := HexToPrivateKey("ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80")

	res, err := c.Auth.RevokeAll(context.Background(), key, "api.rail0.xyz")
	if err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}
	if !revoked {
		t.Fatal("POST /auth/revoke_all was never called")
	}
	if !res.RevokedAll {
		t.Error("RevokedAll = false, want the gateway's true")
	}
	if res.CutoffAt != "2026-08-27T21:00:00Z" {
		t.Errorf("CutoffAt = %q, want the instant the gateway recorded", res.CutoffAt)
	}
	if !strings.Contains(body.Message, "Sign out of RAIL0 everywhere") {
		t.Errorf("the proof must carry the revoke-all statement, got message:\n%s", body.Message)
	}
	if !strings.Contains(body.Message, "abc123nonce9") || !strings.HasPrefix(body.Signature, "0x") {
		t.Errorf("the proof must embed the fresh nonce and be signed, got %+v", body)
	}

	_, _ = c.Health.Get(context.Background())
	if sentAfter != "" && sentAfter != "Bearer " {
		t.Errorf("Authorization after RevokeAll = %q, want it cleared", sentAfter)
	}
}

// A refused revoke-all (a login proof replayed here is 422 siwe_purpose_mismatch)
// revoked nothing, so the client's session is still valid and must be KEPT: clearing
// it would log the caller out for an operation that did not happen.
func TestAuthRevokeAllKeepsTheTokenWhenRefused(t *testing.T) {
	var sentAfter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/nonces":
			_ = json.NewEncoder(w).Encode(map[string]string{"nonce": "abc123nonce9"})
		case "/auth/revoke_all":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"errors":[{"code":"siwe_purpose_mismatch","title":"Unprocessable","detail":"wrong statement"}]}`))
		default:
			sentAfter = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt-abc"})
	key, _ := HexToPrivateKey("ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80")
	if _, err := c.Auth.RevokeAll(context.Background(), key, "api.rail0.xyz"); err == nil {
		t.Fatal("a 422 must surface as an error")
	}
	_, _ = c.Health.Get(context.Background())
	if sentAfter != "Bearer jwt-abc" {
		t.Errorf("Authorization after a refused RevokeAll = %q, want the session kept", sentAfter)
	}
}

// POST /auth carries the account's name on every account session and `admin: true`
// only on an operator's; a standard session has no admin field and decodes false.
func TestAuthVerifyDecodesNameAndTheOperatorGrant(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"standard session", `{"token":"t","address":"0xa","account_id":"acc","name":"Ops","expires_at":"x"}`, false},
		{"admin session", `{"token":"t","address":"0xa","account_id":"acc","name":"Ops","expires_at":"x","admin":true}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			res, err := NewClient(ClientOptions{BaseURL: srv.URL}).Auth.Verify(context.Background(),
				AuthRequest{Message: "m", Signature: "0xs"})
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if res.Name != "Ops" || res.Admin != tc.want {
				t.Errorf("Name = %q, Admin = %v; want %q, %v", res.Name, res.Admin, "Ops", tc.want)
			}
		})
	}
}
