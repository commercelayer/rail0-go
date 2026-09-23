package rail0

import (
	"context"
	"encoding/hex"
	"fmt"

	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	siwe "github.com/spruceid/siwe-go"
)

// AuthService handles SIWE authentication.
type AuthService struct {
	http *httpClient
}

// NonceResponse is returned by GetNonce (POST /auth/nonces). The single-use nonce
// to embed in the SIWE message is carried in Nonce.
type NonceResponse struct {
	Nonce     string `json:"nonce"`
	ExpiresAt string `json:"expires_at"`
}

// AuthRequest is the body for Verify (POST /auth).
type AuthRequest struct {
	Message   string `json:"message"`
	Signature string `json:"signature"`
}

// AuthResponse is returned by Verify on success.
type AuthResponse struct {
	Token     string `json:"token"`
	Address   string `json:"address"`
	AccountID string `json:"account_id"`
	ExpiresAt string `json:"expires_at"`
}

// LogoutResponse is returned by Logout. `Revoked` is the gateway's answer to the
// question that matters: was the token actually added to the denylist? It is
// false when the revocation could not be recorded (e.g. the denylist store was
// unreachable), and then the token REMAINS USABLE until its own expiry — a caller
// that cannot revoke should treat its copy as compromised rather than assume the
// session ended.
type LogoutResponse struct {
	Revoked bool `json:"revoked"`
}

// RevokeAllResponse is the outcome of POST /auth/revoke_all.
//
// CutoffAt is the instant the gateway recorded, and it is the field worth logging: it
// says exactly which sessions died. A boolean cannot — every session of the address
// whose iat precedes this instant is refused from now on, including the ones the
// caller never saw.
//
// The JSON names are the gateway's own (`revoked_all`, `cutoff_at`). This type used
// to read `revoked`/`cutoff`, which the gateway never sent, so even a successful call
// decoded as {false, ""}.
type RevokeAllResponse struct {
	RevokedAll bool   `json:"revoked_all"`
	CutoffAt   string `json:"cutoff_at"`
}

// GetNonce issues a single-use SIWE nonce from the API (POST /auth/nonces).
func (s *AuthService) GetNonce(ctx context.Context) (*NonceResponse, error) {
	var out NonceResponse
	if err := s.http.post(ctx, "/auth/nonces", struct{}{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Verify submits a signed SIWE message and returns a JWT on success. The returned
// token is also stashed on the shared transport, so the SAME client is immediately
// authenticated for the gated endpoints — no manual SetToken needed (equivalent to
// calling Client.SetToken(resp.Token)). Login funnels through here, so it authenticates
// the client too.
func (s *AuthService) Verify(ctx context.Context, req AuthRequest) (*AuthResponse, error) {
	var out AuthResponse
	if err := s.http.post(ctx, "/auth", req, &out); err != nil {
		return nil, err
	}
	if out.Token != "" {
		s.http.SetToken(out.Token)
	}
	return &out, nil
}

// Logout ends the current session by revoking the presented token server-side
// (POST /auth/logout): the gateway records the token's `jti` in its denylist, so
// the JWT stops being accepted before its `exp`.
//
// It requires the client to be authenticated — the token being revoked is the one
// on the transport — and it also CLEARS that token locally, so the client cannot
// keep sending a credential it just asked to have invalidated.
//
// Deleting a stored token without calling this leaves a bearer credential valid
// for the rest of its lifetime: whoever holds a copy keeps the session. That is
// why a caller that persists tokens (the CLI does) should revoke before
// discarding — and before switching to another gateway.
func (s *AuthService) Logout(ctx context.Context) (*LogoutResponse, error) {
	var out LogoutResponse
	if err := s.http.post(ctx, "/auth/logout", struct{}{}, &out); err != nil {
		return nil, err
	}
	s.http.SetToken("")
	return &out, nil
}

// RevokeAll ends EVERY session of the address controlled by privateKey
// (POST /auth/revoke_all).
//
// The answer to a key you no longer trust, and Logout cannot be that answer: Logout is
// per TOKEN, so an address with five live sessions needs five tokens the caller does not
// have. This is per ADDRESS and reaches the ones it has never seen — including any an
// attacker is holding.
//
// Authorized by a FRESH SIWE PROOF of the address, not by the session: someone reacting
// to a leaked key holds the wallet, not the stolen token, so requiring the session would
// make this useless exactly when it is needed. The proof is the same handshake as Login —
// fetch a single-use nonce, build an EIP-4361 message, sign it with EIP-191
// personal_sign — but carries the REVOKE-ALL statement, so a login proof cannot be
// replayed here (the gateway answers 422 siwe_purpose_mismatch). This method used to
// post an empty body, which the gateway refused with 400 on every call. domain/chainID
// carry the same meaning and defaults as Login.
//
// The gateway records a cutoff INSTANT rather than enumerating tokens, and it is
// self-revoking: the cutoff is just after now, so the token this client may hold dies
// with the rest. The local token is therefore cleared on success, exactly as Logout does
// — the client must not keep sending a credential it just had invalidated — and the
// caller signs in again afterwards.
func (s *AuthService) RevokeAll(ctx context.Context, privateKey []byte, domain string, chainID ...int) (*RevokeAllResponse, error) {
	nonceResp, err := s.GetNonce(ctx)
	if err != nil {
		return nil, fmt.Errorf("rail0: get nonce: %w", err)
	}
	message, sig, err := signSIWE(privateKey, domain, nonceResp.Nonce, siweRevokeAllStatement, loginChainID(chainID...))
	if err != nil {
		return nil, err
	}
	var out RevokeAllResponse
	if err := s.http.post(ctx, "/auth/revoke_all", AuthRequest{Message: message, Signature: sig}, &out); err != nil {
		return nil, err
	}
	s.http.SetToken("")
	return &out, nil
}

// Login performs the full SIWE authentication flow:
//  1. Fetches a nonce from the API
//  2. Builds an EIP-4361 compliant message via siwe-go
//  3. Signs it with the provided private key (EIP-191 personal_sign)
//  4. Verifies the signature with the API and returns a JWT
//
// privateKey must be 32 raw bytes — use HexToPrivateKey to convert from hex.
// domain is the host of the API server (e.g. "api.rail0.xyz"). The optional
// chainID (default 1 when omitted) is the chain id embedded in the SIWE message
// and must match the gateway's SIWE_CHAIN_ID policy — override only when the
// gateway is configured with a different login chain.
func (s *AuthService) Login(ctx context.Context, privateKey []byte, domain string, chainID ...int) (*AuthResponse, error) {
	nonceResp, err := s.GetNonce(ctx)
	if err != nil {
		return nil, fmt.Errorf("rail0: get nonce: %w", err)
	}

	message, sig, err := signSIWE(privateKey, domain, nonceResp.Nonce, siweLoginStatement, loginChainID(chainID...))
	if err != nil {
		return nil, err
	}

	return s.Verify(ctx, AuthRequest{Message: message, Signature: sig})
}

// WalletProof is a SIWE proof-of-ownership of an address: the signed EIP-4361
// message plus its signature. It is produced by ProveAddress and submitted to
// WalletsService.Create to register a wallet (the gateway verifies the signature
// recovers to the wallet's address).
type WalletProof struct {
	Message   string
	Signature string
}

// ProveAddress runs the SIWE proof-of-ownership handshake for the address
// controlled by privateKey and returns the {message, signature} to hand to
// WalletsService.Create. It is the same handshake as Login — fetch a single-use
// nonce, build an EIP-4361 message, sign it with EIP-191 personal_sign — but
// stops short of POST /auth: it does NOT authenticate the client, because adding
// a wallet proves control of the ADDED address while the request itself is
// authorized by the caller's existing session (the two addresses may differ,
// e.g. a merchant registering several payee wallets).
//
// The message carries the WALLET-LINK statement, not the login one. This used to
// reuse Login's message verbatim, which the gateway now refuses with 422
// siwe_purpose_mismatch: a login proof is issued on every sign-in, so accepting
// it here would let anyone holding one bind that address to their own account.
//
// privateKey must be the 32-byte key OF the address being added. domain/chainID
// carry the same meaning and defaults as Login (the gateway host and, default 1,
// the nominal login chain) and must satisfy the gateway's SIWE policy.
func (s *AuthService) ProveAddress(ctx context.Context, privateKey []byte, domain string, chainID ...int) (*WalletProof, error) {
	nonceResp, err := s.GetNonce(ctx)
	if err != nil {
		return nil, fmt.Errorf("rail0: get nonce: %w", err)
	}

	message, sig, err := signSIWE(privateKey, domain, nonceResp.Nonce, siweWalletLinkStatement, loginChainID(chainID...))
	if err != nil {
		return nil, err
	}

	return &WalletProof{Message: message, Signature: sig}, nil
}

// loginChainID resolves the optional variadic chain id to a concrete value,
// defaulting to 1 (the gateway's default SIWE login chain) when omitted or zero.
func loginChainID(chainID ...int) int {
	if len(chainID) > 0 && chainID[0] != 0 {
		return chainID[0]
	}
	return 1
}

// SIWE statements, one per PURPOSE. The gateway binds each endpoint to exactly
// one of these (Policy::SIWE_LOGIN_STATEMENT / SIWE_WALLET_LINK_STATEMENT /
// SIWE_REVOKE_ALL_STATEMENT) and rejects the others with 422 siwe_purpose_mismatch.
//
// They are not interchangeable and must not be collapsed back into one. A login
// signature is handed out on every sign-in; if the wallet-link endpoint also
// accepted it, anyone holding one captured login proof could bind that address
// to THEIR account. The distinct statement is what makes a proof spendable at
// exactly one endpoint.
const (
	siweLoginStatement      = "Sign in to RAIL0"
	siweWalletLinkStatement = "Add this wallet to your RAIL0 account"
	siweRevokeAllStatement  = "Sign out of RAIL0 everywhere"
)

// signSIWE builds an EIP-4361 message for the address controlled by privateKey
// (domain, uri "https://"+domain, the given single-use nonce, the given chainId
// and the given purpose statement) and signs it with EIP-191 personal_sign. It
// is the shared core of the SIWE handshake: Login uses it to prove the session
// address, ProveAddress to prove a wallet being added. Everything but the
// statement is identical between the two. Returns the message text and its
// 0x-prefixed signature.
func signSIWE(privateKey []byte, domain, nonce, statement string, chainID int) (message, signature string, err error) {
	address, err := privateKeyToEthAddress(privateKey)
	if err != nil {
		return "", "", fmt.Errorf("rail0: derive address: %w", err)
	}

	msg, err := siwe.InitMessage(domain, address, "https://"+domain, nonce, map[string]interface{}{
		"chainId":   chainID,
		"statement": statement,
	})
	if err != nil {
		return "", "", fmt.Errorf("rail0: build SIWE message: %w", err)
	}
	message = msg.String()

	signature, err = personalSign(privateKey, message)
	if err != nil {
		return "", "", fmt.Errorf("rail0: sign: %w", err)
	}
	return message, signature, nil
}

// privateKeyToEthAddress derives the EIP-55 checksummed Ethereum address.
func privateKeyToEthAddress(privateKey []byte) (string, error) {
	if len(privateKey) != 32 {
		return "", fmt.Errorf("private key must be 32 bytes")
	}
	privKey := secp.PrivKeyFromBytes(privateKey)
	pubBytes := privKey.PubKey().SerializeUncompressed() // 0x04 + X(32) + Y(32)
	hash := keccak256(pubBytes[1:])
	return checksumAddress(hash[12:]), nil
}

// checksumAddress applies EIP-55 checksum encoding to 20 raw address bytes.
func checksumAddress(b []byte) string {
	lower := hex.EncodeToString(b)
	hash := keccak256([]byte(lower))
	out := make([]byte, len(lower))
	for i := range lower {
		c := lower[i]
		if c >= 'a' && c <= 'f' && hash[i/2]>>(4-uint(i%2)*4)&0xf >= 8 {
			out[i] = c - 32
		} else {
			out[i] = c
		}
	}
	return "0x" + string(out)
}

// personalSign signs a message with the Ethereum EIP-191 personal_sign prefix
// and returns a 0x-prefixed hex signature (65 bytes: R || S || V, V in {27,28}).
func personalSign(privateKey []byte, message string) (string, error) {
	prefixed := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)
	digest := keccak256([]byte(prefixed))

	privKey := secp.PrivKeyFromBytes(privateKey)
	// SignCompact: [v(1)] [r(32)] [s(32)], isCompressedKey=false → v ∈ {27,28}
	compact := ecdsa.SignCompact(privKey, digest, false)
	// Reorder to r || s || v as expected by Ethereum/SIWE
	sig := make([]byte, 65)
	copy(sig[0:32], compact[1:33]) // r
	copy(sig[32:64], compact[33:]) // s
	sig[64] = compact[0]           // v
	return "0x" + hex.EncodeToString(sig), nil
}
