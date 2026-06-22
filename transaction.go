package rail0

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// unsignedTx mirrors the EIP-1559 field set the gateway returns as a
// transaction's `unsigned_transaction` (a JSON string). Numbers are decoded as
// json.Number so wei-scale values (value, fees) keep full precision before
// conversion to big.Int — a plain float64 would lose precision past 2^53.
type unsignedTx struct {
	ChainID              json.Number `json:"chain_id"`
	To                   string      `json:"to"`
	Value                json.Number `json:"value"`
	Data                 string      `json:"data"`
	Nonce                json.Number `json:"nonce"`
	GasLimit             json.Number `json:"gas_limit"`
	MaxPriorityFeePerGas json.Number `json:"max_priority_fee_per_gas"`
	MaxFeePerGas         json.Number `json:"max_fee_per_gas"`
}

// SignTransaction builds and signs the EIP-1559 (type-2) transaction described
// by the gateway's `unsigned_transaction` JSON and returns the signed raw
// transaction as a 0x-prefixed hex string, ready for the submit endpoints.
//
// The gateway never holds private keys: it returns the transaction *fields*
// (chain id, nonce, to, value, data, gas, fees) and the client assembles the
// type-2 transaction, computes its signing hash (keccak256 over the RLP
// payload) and signs locally. privateKey is 32 raw bytes (see HexToPrivateKey).
func SignTransaction(unsignedJSON string, privateKey []byte) (string, error) {
	var u unsignedTx
	if err := json.Unmarshal([]byte(unsignedJSON), &u); err != nil {
		return "", fmt.Errorf("rail0: parse unsigned transaction: %w", err)
	}

	chainID, err := bigIntField("chain_id", u.ChainID)
	if err != nil {
		return "", err
	}
	value, err := bigIntField("value", u.Value)
	if err != nil {
		return "", err
	}
	tip, err := bigIntField("max_priority_fee_per_gas", u.MaxPriorityFeePerGas)
	if err != nil {
		return "", err
	}
	feeCap, err := bigIntField("max_fee_per_gas", u.MaxFeePerGas)
	if err != nil {
		return "", err
	}
	nonce, err := strconv.ParseUint(u.Nonce.String(), 10, 64)
	if err != nil {
		return "", fmt.Errorf("rail0: invalid nonce %q: %w", u.Nonce, err)
	}
	gas, err := strconv.ParseUint(u.GasLimit.String(), 10, 64)
	if err != nil {
		return "", fmt.Errorf("rail0: invalid gas_limit %q: %w", u.GasLimit, err)
	}
	if !common.IsHexAddress(u.To) {
		return "", fmt.Errorf("rail0: invalid to address %q", u.To)
	}
	to := common.HexToAddress(u.To)

	privKey, err := crypto.ToECDSA(privateKey)
	if err != nil {
		return "", fmt.Errorf("rail0: invalid private key: %w", err)
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: tip,
		GasFeeCap: feeCap,
		Gas:       gas,
		To:        &to,
		Value:     value,
		Data:      common.FromHex(u.Data),
	})

	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), privKey)
	if err != nil {
		return "", fmt.Errorf("rail0: sign transaction: %w", err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("rail0: encode signed transaction: %w", err)
	}
	return "0x" + hex.EncodeToString(raw), nil
}

// bigIntField parses a base-10 JSON number into a big.Int, preserving the full
// precision of wei-scale values.
func bigIntField(name string, n json.Number) (*big.Int, error) {
	v, ok := new(big.Int).SetString(n.String(), 10)
	if !ok {
		return nil, fmt.Errorf("rail0: invalid %s %q", name, n)
	}
	return v, nil
}
