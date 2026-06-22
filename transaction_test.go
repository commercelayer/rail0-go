package rail0

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestSignTransaction(t *testing.T) {
	// Deterministic test key (well-known Hardhat account #0).
	const keyHex = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	key, err := HexToPrivateKey(keyHex)
	if err != nil {
		t.Fatalf("HexToPrivateKey: %v", err)
	}
	priv, _ := crypto.HexToECDSA(keyHex)
	wantFrom := crypto.PubkeyToAddress(priv.PublicKey)

	// Shape mirrors the gateway's unsigned_transaction (wei-scale fee fields).
	const unsigned = `{"chain_id":5042002,"from":"0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266",` +
		`"to":"0x4CCC4DdeBB8A63B9186936A8C0FA404910A4311b","value":0,"data":"0xabcdef",` +
		`"nonce":275,"gas_limit":200000,"max_priority_fee_per_gas":37423102,"max_fee_per_gas":80187115510}`

	rawHex, err := SignTransaction(unsigned, key)
	if err != nil {
		t.Fatalf("SignTransaction: %v", err)
	}
	if !strings.HasPrefix(rawHex, "0x02") {
		t.Fatalf("want EIP-1559 type-2 tx (0x02 prefix), got %.6s", rawHex)
	}

	var tx types.Transaction
	if err := tx.UnmarshalBinary(common.FromHex(rawHex)); err != nil {
		t.Fatalf("decode signed tx: %v", err)
	}

	// Recovered sender must match the signing key.
	from, err := types.Sender(types.LatestSignerForChainID(big.NewInt(5042002)), &tx)
	if err != nil {
		t.Fatalf("recover sender: %v", err)
	}
	if from != wantFrom {
		t.Errorf("sender = %s, want %s", from, wantFrom)
	}

	// Fields round-trip.
	if tx.Type() != types.DynamicFeeTxType {
		t.Errorf("type = %d, want %d", tx.Type(), types.DynamicFeeTxType)
	}
	if tx.Nonce() != 275 {
		t.Errorf("nonce = %d, want 275", tx.Nonce())
	}
	if tx.Gas() != 200000 {
		t.Errorf("gas = %d, want 200000", tx.Gas())
	}
	if got := tx.GasFeeCap().Int64(); got != 80187115510 {
		t.Errorf("maxFeePerGas = %d, want 80187115510", got)
	}
	if tx.To() == nil || *tx.To() != common.HexToAddress("0x4CCC4DdeBB8A63B9186936A8C0FA404910A4311b") {
		t.Errorf("to = %v, want 0x4CCC…4311b", tx.To())
	}
}

func TestSignTransactionRejectsBadJSON(t *testing.T) {
	key, _ := HexToPrivateKey("ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80")
	if _, err := SignTransaction("{not json", key); err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}
