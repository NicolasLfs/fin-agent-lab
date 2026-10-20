package domain

import (
	"encoding/json"
	"testing"
)

func TestNewMoneyPreservesExactMinorUnits(t *testing.T) {
	tests := []struct {
		name     string
		amount   string
		currency string
		want     string
	}{
		{name: "whole amount", amount: "10000", currency: "BRL", want: "10000.00"},
		{name: "one fractional digit", amount: "10000.5", currency: "BRL", want: "10000.50"},
		{name: "two fractional digits", amount: "0.01", currency: "BRL", want: "0.01"},
		{name: "negative amount", amount: "-12.34", currency: "USD", want: "-12.34"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money, err := NewMoney(tt.amount, tt.currency)
			if err != nil {
				t.Fatalf("NewMoney() error = %v", err)
			}
			if got := money.Amount(); got != tt.want {
				t.Errorf("Amount() = %q, want %q", got, tt.want)
			}
			if got := money.Currency(); got != tt.currency {
				t.Errorf("Currency() = %q, want %q", got, tt.currency)
			}
		})
	}
}

func TestNewMoneyRejectsInvalidAmountAndCurrency(t *testing.T) {
	tests := []struct {
		name     string
		amount   string
		currency string
	}{
		{name: "NaN", amount: "NaN", currency: "BRL"},
		{name: "scientific notation", amount: "1e3", currency: "BRL"},
		{name: "too many decimal places", amount: "1.001", currency: "BRL"},
		{name: "empty amount", amount: "", currency: "BRL"},
		{name: "lowercase currency", amount: "1.00", currency: "brl"},
		{name: "short currency", amount: "1.00", currency: "BR"},
		{name: "non-letter currency", amount: "1.00", currency: "B1L"},
		{name: "unrecognized currency", amount: "1.00", currency: "ZZZ"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewMoney(tt.amount, tt.currency); err == nil {
				t.Fatal("NewMoney() error = nil, want invalid input error")
			}
		})
	}
}

func TestNewMoneyRejectsValuesOutsideMinorUnitRange(t *testing.T) {
	if _, err := NewMoney("92233720368547758.08", "BRL"); err == nil {
		t.Fatal("NewMoney() error = nil, want overflow error")
	}
	if _, err := NewMoney("-92233720368547758.09", "BRL"); err == nil {
		t.Fatal("NewMoney() error = nil, want underflow error")
	}
	for _, amount := range []string{"92233720368547758.07", "-92233720368547758.08"} {
		if _, err := NewMoney(amount, "BRL"); err != nil {
			t.Errorf("NewMoney(%q) error = %v, want supported boundary value", amount, err)
		}
	}
}

func TestMoneyJSONContract(t *testing.T) {
	money, err := NewMoney("10000.5", "BRL")
	if err != nil {
		t.Fatalf("NewMoney() error = %v", err)
	}

	encoded, err := json.Marshal(money)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if got, want := string(encoded), `{"amount":"10000.50","currency":"BRL"}`; got != want {
		t.Errorf("json.Marshal() = %s, want %s", got, want)
	}

	var decoded Money
	if err := json.Unmarshal([]byte(`{"amount":"10000.50","currency":"BRL"}`), &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got := decoded.Amount(); got != "10000.50" {
		t.Errorf("decoded Amount() = %q, want %q", got, "10000.50")
	}
	if got := decoded.Currency(); got != "BRL" {
		t.Errorf("decoded Currency() = %q, want %q", got, "BRL")
	}
}

func TestMoneyJSONRejectsInvalidShapes(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "numeric amount", data: `{"amount":1.00,"currency":"BRL"}`},
		{name: "NaN", data: `{"amount":"NaN","currency":"BRL"}`},
		{name: "scientific notation", data: `{"amount":"1e3","currency":"BRL"}`},
		{name: "excess scale", data: `{"amount":"1.001","currency":"BRL"}`},
		{name: "missing currency", data: `{"amount":"1.00"}`},
		{name: "unknown field", data: `{"amount":"1.00","currency":"BRL","value":1}`},
		{name: "not an object", data: `"1.00"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var money Money
			if err := json.Unmarshal([]byte(tt.data), &money); err == nil {
				t.Fatal("json.Unmarshal() error = nil, want invalid JSON error")
			}
		})
	}
}
