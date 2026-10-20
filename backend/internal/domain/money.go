package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"

	textcurrency "golang.org/x/text/currency"
)

var amountPattern = regexp.MustCompile(`^(-?)(0|[1-9][0-9]*)(?:\.([0-9]{1,2}))?$`)

var errInvalidCurrency = errors.New("currency must be a three-letter uppercase ISO 4217 code")

type Money struct {
	minorUnits int64
	currency   string
}

func NewMoney(amount, currency string) (Money, error) {
	if !isCurrencyCode(currency) {
		return Money{}, errInvalidCurrency
	}

	matches := amountPattern.FindStringSubmatch(amount)
	if matches == nil {
		return Money{}, fmt.Errorf("invalid amount %q: expected a decimal value with at most two fractional digits", amount)
	}

	units, ok := new(big.Int).SetString(matches[2], 10)
	if !ok {
		return Money{}, fmt.Errorf("invalid amount %q", amount)
	}
	units.Mul(units, big.NewInt(100))

	fraction := matches[3]
	if len(fraction) == 1 {
		fraction += "0"
	}
	if fraction != "" {
		minor, ok := new(big.Int).SetString(fraction, 10)
		if !ok {
			return Money{}, fmt.Errorf("invalid amount %q", amount)
		}
		units.Add(units, minor)
	}
	if matches[1] == "-" {
		units.Neg(units)
	}
	if !units.IsInt64() {
		return Money{}, fmt.Errorf("amount %q exceeds the supported range", amount)
	}

	return Money{minorUnits: units.Int64(), currency: currency}, nil
}

func (m Money) Amount() string {
	units := big.NewInt(m.minorUnits)
	negative := units.Sign() < 0
	if negative {
		units.Abs(units)
	}

	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(units, big.NewInt(100), fraction)

	sign := ""
	if negative {
		sign = "-"
	}
	return fmt.Sprintf("%s%s.%02d", sign, whole.String(), fraction.Int64())
}

func (m Money) Currency() string {
	return m.currency
}

func (m Money) MarshalJSON() ([]byte, error) {
	if !isCurrencyCode(m.currency) {
		return nil, errInvalidCurrency
	}

	return json.Marshal(struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}{
		Amount:   m.Amount(),
		Currency: m.currency,
	})
}

func (m *Money) UnmarshalJSON(data []byte) error {
	if m == nil {
		return errors.New("cannot unmarshal Money into a nil receiver")
	}

	var payload struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("decode Money: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode Money: unexpected trailing JSON value")
		}
		return fmt.Errorf("decode Money: %w", err)
	}

	parsed, err := NewMoney(payload.Amount, payload.Currency)
	if err != nil {
		return fmt.Errorf("decode Money: %w", err)
	}
	*m = parsed
	return nil
}

func isCurrencyCode(currency string) bool {
	if len(currency) != 3 {
		return false
	}
	for _, char := range currency {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	_, err := textcurrency.ParseISO(currency)
	return err == nil
}
