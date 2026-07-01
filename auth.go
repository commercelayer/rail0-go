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
// value to embed in the SIWE message is carried in Value.
type NonceResponse struct {
	ID        string `json:"id"`
	Value     string `json:"value"`
	ExpiresAt string `json:"expires_at"`
	Used      bool   `json:"used"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at,omitempty"`
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

// Verify submits a signed SIWE message and returns a JWT on success.
func (s *AuthService) Verify(ctx context.Context, req AuthRequest) (*AuthResponse, error) {
	var out AuthResponse
	if err := s.http.post(ctx, "/auth", req, &out); err != nil {
		return nil, err
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
// domain is the host of the API server (e.g. "api.rail0.xyz").
func (s *AuthService) Login(ctx context.Context, privateKey []byte, domain string) (*AuthResponse, error) {
	nonceResp, err := s.GetNonce(ctx)
	if err != nil {
		return nil, fmt.Errorf("rail0: get nonce: %w", err)
	}

	address, err := privateKeyToEthAddress(privateKey)
	if err != nil {
		return nil, fmt.Errorf("rail0: derive address: %w", err)
	}

	msg, err := siwe.InitMessage(domain, address, "https://"+domain, nonceResp.Value, map[string]interface{}{
		"chainId":   1,
		"statement": "Sign in to RAIL0",
	})
	if err != nil {
		return nil, fmt.Errorf("rail0: build SIWE message: %w", err)
	}
	messageStr := msg.String()

	sig, err := personalSign(privateKey, messageStr)
	if err != nil {
		return nil, fmt.Errorf("rail0: sign: %w", err)
	}

	return s.Verify(ctx, AuthRequest{Message: messageStr, Signature: sig})
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
