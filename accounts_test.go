package rail0

import (
	"encoding/json"
	"testing"
)

// The account read has one shape and one caller: the holder. Email is in the response
// because the gateway's ownership guard makes the holder its only possible caller, so the
// SDK does not narrow it away.
func TestAccountDecodesTheHoldersProfile(t *testing.T) {
	const body = `{
	  "id": "019f8a3d-b781-7b00-8b75-8427f7e591d2",
	  "name": "Test Merchant",
	  "email": "merchant@rail0.test",
	  "created_at": "2026-08-01T00:00:00Z",
	  "updated_at": "2026-08-20T00:00:00Z"
	}`

	var account Account
	if err := json.Unmarshal([]byte(body), &account); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if account.Email != "merchant@rail0.test" || account.Name != "Test Merchant" {
		t.Errorf("profile not decoded: %+v", account)
	}
}

// The sender is the field that makes a release attributable, and it is legitimately absent
// on a report-by-hash submit — so the zero value has to mean "not known", not "nobody".
func TestTransactionSenderIsOptional(t *testing.T) {
	var withSender, without Transaction
	if err := json.Unmarshal([]byte(`{"operation":"release","sender":"0xabc"}`), &withSender); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"operation":"release"}`), &without); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if withSender.Sender != "0xabc" {
		t.Errorf("Sender = %q, want 0xabc", withSender.Sender)
	}
	if without.Sender != "" {
		t.Errorf("absent Sender should be the zero value, got %q", without.Sender)
	}
}
