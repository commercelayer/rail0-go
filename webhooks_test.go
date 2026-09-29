package rail0

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Redeliver is a bodiless POST on the callback's redeliver path, and the gateway's 202
// {"status":"queued"} must decode into the typed result: queued is all the endpoint can
// promise, since the replay itself is delivered asynchronously.
func TestWebhooksRedeliverQueuesTheRecordedCallback(t *testing.T) {
	const webhookID = "019f8a3d-0000-7000-8000-000000000001"
	const callbackID = "019f8a3d-0000-7000-8000-000000000002"
	var gotMethod, gotPath string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"queued"}`))
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt"})
	res, err := c.Webhooks.Redeliver(context.Background(), webhookID, callbackID)
	if err != nil {
		t.Fatalf("Redeliver: %v", err)
	}
	wantPath := "/webhooks/" + webhookID + "/event_callbacks/" + callbackID + "/redeliver"
	if gotMethod != http.MethodPost || gotPath != wantPath {
		t.Errorf("request = %s %s, want POST %s", gotMethod, gotPath, wantPath)
	}
	if len(gotBody) != 0 {
		t.Errorf("request body = %q, want none", gotBody)
	}
	if res.Status != "queued" {
		t.Errorf("Status = %q, want queued", res.Status)
	}
}

// A callback with nothing to replay (or not the caller's) is a 404 from the gateway and
// must surface as an error, not as an empty result that reads like success.
func TestWebhooksRedeliverSurfacesNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"not_found","title":"Not found","detail":"event_callback not found."}`))
	}))
	defer srv.Close()

	c := NewClient(ClientOptions{BaseURL: srv.URL, Token: "jwt"})
	res, err := c.Webhooks.Redeliver(context.Background(), "wh", "cb")
	if err == nil {
		t.Fatalf("Redeliver = %+v, want an error", res)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Errorf("err = %v, want a 404 *APIError", err)
	}
}
