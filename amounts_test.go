package rail0

import "testing"

func TestToBaseUnits(t *testing.T) {
	cases := []struct {
		human    string
		decimals int
		want     string
		wantErr  bool
	}{
		{"1.5", 6, "1500000", false},
		{"1", 6, "1000000", false},
		{"0.000001", 6, "1", false},
		{"1.2345678", 6, "1234567", false}, // extra fractional digits truncated
		{"0", 6, "0", false},
		{"0.0", 6, "0", false},
		{"10", 0, "10", false},
		{"1.5", 18, "1500000000000000000", false},
		{"", 6, "", true},
		{"1.2.3", 6, "", true},
		{"abc", 6, "", true},
		{"-1", 6, "", true},
		{"1", -1, "", true},
	}
	for _, c := range cases {
		got, err := ToBaseUnits(c.human, c.decimals)
		if c.wantErr {
			if err == nil {
				t.Errorf("ToBaseUnits(%q, %d): expected error, got %q", c.human, c.decimals, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ToBaseUnits(%q, %d): unexpected error %v", c.human, c.decimals, err)
		} else if got != c.want {
			t.Errorf("ToBaseUnits(%q, %d) = %q, want %q", c.human, c.decimals, got, c.want)
		}
	}
}

func TestFormatAmount(t *testing.T) {
	cases := []struct {
		base     string
		decimals int
		want     string
	}{
		{"1500000", 6, "1.5"},
		{"2000000", 6, "2"},
		{"1", 6, "0.000001"},
		{"0", 6, "0"},
		{"1234567", 6, "1.234567"},
		{"1500000000000000000", 18, "1.5"},
		{"10", 0, "10"},
		{"notanumber", 6, "notanumber"}, // returned unchanged
	}
	for _, c := range cases {
		if got := FormatAmount(c.base, c.decimals); got != c.want {
			t.Errorf("FormatAmount(%q, %d) = %q, want %q", c.base, c.decimals, got, c.want)
		}
	}
}

func TestToBaseUnitsRoundTrip(t *testing.T) {
	for _, h := range []string{"1.5", "0.000001", "1000", "0.25"} {
		b, err := ToBaseUnits(h, 6)
		if err != nil {
			t.Fatalf("ToBaseUnits(%q): %v", h, err)
		}
		// Formatting the base units back yields a value that re-parses to the same base.
		b2, err := ToBaseUnits(FormatAmount(b, 6), 6)
		if err != nil || b2 != b {
			t.Errorf("round trip for %q: %q -> %q", h, b, b2)
		}
	}
}

func TestDescribeError(t *testing.T) {
	if DescribeError("refund_expired") == "" {
		t.Error("expected a hint for refund_expired")
	}
	if DescribeError("totally_unknown_code") != "" {
		t.Error("expected no hint for an unknown code")
	}
	e := &APIError{Code: "contract_revert", Reason: "not_payee"}
	if e.Hint() == "" {
		t.Error("expected Hint() to fall back to the Reason sub-cause")
	}
}
