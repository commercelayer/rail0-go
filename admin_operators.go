package rail0

import "context"

// The operator role itself: who holds it, granting it and revoking it. The role is a
// grant, a record beside the account (not a flag on it), with who vouched for it, why,
// and optionally until when.
//
// Both writes need a write-mode token (see AdminService) and are audited on the gateway.
// Two refusals protect the role from a slip or from a stolen session: an operator cannot
// revoke their OWN grant, and the last active operator cannot be revoked at all.

// Operators lists every operator grant, newest first, with the account that holds it
// (Account) and the operator who granted it (Grantor, nil for a grant made out of band:
// the seeds or the gateway's own maintenance task). Expired grants are listed too: the
// record stays as history. A grant opens the operator surface while ExpiresAt is empty or
// still ahead, and its account is active.
func (s *AdminService) Operators(ctx context.Context) ([]AdministratorFull, error) {
	var out []AdministratorFull
	if err := s.http.get(ctx, "/admin/administrators", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GrantOperatorRequest is the context of a grant. Both fields are optional.
type GrantOperatorRequest struct {
	// Note says why the grant exists. Free text, kept with the grant and in the audit log.
	Note string `json:"note,omitempty"`
	// ExpiresAt time-boxes the grant (ISO-8601): past it the grant no longer opens the
	// operator surface. Empty means no expiry. A time not in the future is refused with
	// 422 `expiry_in_the_past`.
	ExpiresAt string `json:"expires_at,omitempty"`
}

// GrantOperator gives an account the operator role: from its next request it reads every
// account, and with write mode writes to them. The session's account is recorded as the
// one who granted it.
//
// An account that already holds a grant, expired or not, answers 409 `conflict`: revoke
// it first to change its note or its expiry.
func (s *AdminService) GrantOperator(ctx context.Context, accountID string, req GrantOperatorRequest) (*AdministratorFull, error) {
	var out AdministratorFull
	if err := s.http.post(ctx, pathf("/admin/administrators/%s", accountID), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeOperator removes an account's operator grant. accountID is the ACCOUNT's id, not
// the grant's. The gateway refuses the session's own account (422 `self_revoke`: stepping
// down is another operator's act) and the last active operator (422 `last_admin`). An
// account with no grant answers 404.
func (s *AdminService) RevokeOperator(ctx context.Context, accountID string) error {
	return s.http.delete(ctx, pathf("/admin/administrators/%s", accountID), nil)
}
