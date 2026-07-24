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

	message, sig, err := signSIWE(privateKey, domain, nonceResp.Nonce, loginChainID(chainID...))
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
// WalletsService.Create. It is the exact same handshake as Login — fetch a
// single-use nonce, build the identical EIP-4361 message, sign it with
// EIP-191 personal_sign — but stops short of POST /auth: it does NOT authenticate
// the client, because adding a wallet proves control of the ADDED address while
// the request itself is authorized by the caller's existing session (the two
// addresses may differ, e.g. a merchant registering several payee wallets).
//
// privateKey must be the 32-byte key OF the address being added. domain/chainID
// carry the same meaning and defaults as Login (the gateway host and, default 1,
// the nominal login chain) and must satisfy the gateway's SIWE policy.
func (s *AuthService) ProveAddress(ctx context.Context, privateKey []byte, domain string, chainID ...int) (*WalletProof, error) {
	nonceResp, err := s.GetNonce(ctx)
	if err != nil {
		return nil, fmt.Errorf("rail0: get nonce: %w", err)
	}

	message, sig, err := signSIWE(privateKey, domain, nonceResp.Nonce, loginChainID(chainID...))
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

// signSIWE builds an EIP-4361 message for the address controlled by privateKey
// (domain, uri "https://"+domain, the given single-use nonce, the given chainId,
// statement "Sign in to RAIL0") and signs it with EIP-191 personal_sign. It is
// the shared core of the SIWE handshake: Login uses it to prove the session
// address, ProveAddress to prove a wallet being added — so both produce a byte-
// identical message shape and the gateway verifies them the same way. Returns
// the message text and its 0x-prefixed signature.
func signSIWE(privateKey []byte, domain, nonce string, chainID int) (message, signature string, err error) {
	address, err := privateKeyToEthAddress(privateKey)
	if err != nil {
		return "", "", fmt.Errorf("rail0: derive address: %w", err)
	}

	msg, err := siwe.InitMessage(domain, address, "https://"+domain, nonce, map[string]interface{}{
		"chainId":   chainID,
		"statement": "Sign in to RAIL0",
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
