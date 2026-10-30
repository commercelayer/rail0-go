package rail0

import (
	"strings"
	"testing"
)

func TestResolveChain(t *testing.T) {
	chains := []Blockchain{
		{ChainID: 5042002, Slug: "arc", Name: "Arc Testnet", NetworkType: "testnet"},
		{ChainID: 84532, Slug: "base", Name: "Base Sepolia", NetworkType: "testnet"},
	}
	for ref, want := range map[string]int{"arc": 5042002, "ARC": 5042002, " Base ": 84532, "84532": 84532, "5042002": 5042002} {
		c, err := ResolveChain(chains, ref)
		if err != nil || c.ChainID != want {
			t.Errorf("ResolveChain(%q) = %+v, %v; want chain %d", ref, c, err, want)
		}
	}
	// The pointer is into the caller's slice, not a copy.
	if c, _ := ResolveChain(chains, "arc"); c != &chains[0] {
		t.Error("ResolveChain returned a copy")
	}

	for _, tc := range []struct{ ref, msg string }{
		{"", "no chain given"},
		{"polygon", `no chain with slug "polygon" (available: arc (5042002), base (84532))`},
		{"1", "no chain with id 1 (available: arc (5042002), base (84532))"},
	} {
		if _, err := ResolveChain(chains, tc.ref); err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("ResolveChain(%q) err = %v, want %q", tc.ref, err, tc.msg)
		}
	}
	if _, err := ResolveChain(nil, "arc"); err == nil || !strings.Contains(err.Error(), "available: none") {
		t.Errorf("empty catalog err = %v", err)
	}

	mixed := append(chains, Blockchain{ChainID: 5042000, Slug: "arc", NetworkType: "mainnet"})
	_, err := ResolveChain(mixed, "Arc")
	if err == nil || !strings.Contains(err.Error(), `slug "Arc" matches 2 chains`) || !strings.Contains(err.Error(), "5042000 (mainnet)") {
		t.Errorf("ambiguous slug err = %v", err)
	}
	// An id still resolves in a mixed catalog.
	if c, err := ResolveChain(mixed, "5042000"); err != nil || c.NetworkType != "mainnet" {
		t.Errorf("id in mixed catalog = %+v, %v", c, err)
	}
}
