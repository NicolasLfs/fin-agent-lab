package marketworker

import (
	"math/big"
	"testing"
)

func TestNumericFromDecimalPreservesExactScale(t *testing.T) {
	value, err := numericFromDecimal("0.08956719")
	if err != nil {
		t.Fatalf("numericFromDecimal() error = %v", err)
	}
	if !value.Valid || value.Exp != -8 || value.Int.Cmp(big.NewInt(8956719)) != 0 {
		t.Fatalf("numeric = %#v, want exact 0.08956719", value)
	}
}

func TestNumericFromDecimalRejectsExponentAndMalformedValues(t *testing.T) {
	for _, invalid := range []string{"1e-2", "NaN", "01.2", " 0.5"} {
		if _, err := numericFromDecimal(invalid); err == nil {
			t.Errorf("numericFromDecimal(%q) error = nil, want validation error", invalid)
		}
	}
	if value, err := numericFromDecimal(""); err != nil || value.Valid {
		t.Errorf("numericFromDecimal(empty) = %#v, %v; want SQL NULL", value, err)
	}
}
