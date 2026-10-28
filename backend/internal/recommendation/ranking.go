package recommendation

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"

	"fin-agent/internal/httpapi"
)

const referenceRateHorizonDays = 1095

var decimalPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)

type Candidate struct {
	Ticker           string
	Type             httpapi.AssetType
	GrossAnnualRate  string
	PriceToEarnings  string
	DividendYieldTTM string
}

type scoredCandidate struct {
	asset       httpapi.RankedAsset
	score       *big.Rat
	growth      *big.Rat
	isReference bool
}

func CalculateNetReferenceRate(grossAnnualRate string, days int) (string, error) {
	if days != referenceRateHorizonDays {
		return "", fmt.Errorf("reference rate horizon must be %d days", referenceRateHorizonDays)
	}
	netGrowth, err := netReferenceGrowth(grossAnnualRate)
	if err != nil {
		return "", err
	}
	return roundedCubeRootMinusOne(netGrowth), nil
}

func netReferenceGrowth(grossAnnualRate string) (*big.Rat, error) {
	grossRate, err := parseDecimal(grossAnnualRate)
	if err != nil {
		return nil, fmt.Errorf("parse gross annual reference rate: %w", err)
	}
	if grossRate.Sign() < 0 {
		return nil, errors.New("gross annual reference rate cannot be negative")
	}

	annualGrowth := new(big.Rat).Add(big.NewRat(1, 1), grossRate)
	grossGrowth := new(big.Rat).Mul(annualGrowth, annualGrowth)
	grossGrowth.Mul(grossGrowth, annualGrowth)
	netGrowth := new(big.Rat).Mul(grossGrowth, big.NewRat(17, 20))
	netGrowth.Add(netGrowth, big.NewRat(3, 20))
	return netGrowth, nil
}

func RankCandidates(candidates []Candidate) ([]httpapi.RankedAsset, error) {
	scored := make([]scoredCandidate, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		ticker := strings.TrimSpace(candidate.Ticker)
		if ticker == "" {
			return nil, errors.New("candidate ticker cannot be empty")
		}
		if _, exists := seen[ticker]; exists {
			return nil, fmt.Errorf("duplicate candidate ticker %q", ticker)
		}
		seen[ticker] = struct{}{}

		item, include, err := scoreCandidate(candidate, ticker)
		if err != nil {
			return nil, fmt.Errorf("score candidate %q: %w", ticker, err)
		}
		if include {
			scored = append(scored, item)
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		comparison := compareScores(scored[i], scored[j])
		if comparison == 0 {
			return scored[i].asset.Ticker < scored[j].asset.Ticker
		}
		return comparison > 0
	})

	ranked := make([]httpapi.RankedAsset, len(scored))
	for index := range scored {
		ranked[index] = scored[index].asset
		ranked[index].Rank = index + 1
	}
	return ranked, nil
}

func scoreCandidate(candidate Candidate, ticker string) (scoredCandidate, bool, error) {
	switch candidate.Type {
	case httpapi.AssetTypeFixedIncome:
		if candidate.GrossAnnualRate == "" {
			return scoredCandidate{}, false, nil
		}
		netGrowth, err := netReferenceGrowth(candidate.GrossAnnualRate)
		if err != nil {
			return scoredCandidate{}, false, err
		}
		netRate := roundedCubeRootMinusOne(netGrowth)
		score, err := parseDecimal(netRate)
		if err != nil {
			return scoredCandidate{}, false, err
		}
		return scoredCandidate{
			asset: httpapi.RankedAsset{
				Ticker: ticker, Type: candidate.Type, Score: netRate, ScoreMetric: "REFERENCE_INDEX_NET_RATE",
			},
			score: score, growth: netGrowth, isReference: true,
		}, true, nil
	case httpapi.AssetTypeStock:
		if candidate.PriceToEarnings == "" {
			return scoredCandidate{}, false, nil
		}
		ratio, err := parseDecimal(candidate.PriceToEarnings)
		if err != nil {
			return scoredCandidate{}, false, fmt.Errorf("parse price-to-earnings ratio: %w", err)
		}
		if ratio.Sign() <= 0 {
			return scoredCandidate{}, false, nil
		}
		score := new(big.Rat).Inv(ratio)
		return scoredCandidate{
			asset: httpapi.RankedAsset{
				Ticker: ticker, Type: candidate.Type, Score: formatDecimal(score, 4), ScoreMetric: "EARNINGS_YIELD",
			},
			score: score,
		}, true, nil
	case httpapi.AssetTypeFII:
		if candidate.DividendYieldTTM == "" {
			return scoredCandidate{}, false, nil
		}
		score, err := parseDecimal(candidate.DividendYieldTTM)
		if err != nil {
			return scoredCandidate{}, false, fmt.Errorf("parse TTM dividend yield: %w", err)
		}
		if score.Sign() < 0 {
			return scoredCandidate{}, false, errors.New("TTM dividend yield cannot be negative")
		}
		return scoredCandidate{
			asset: httpapi.RankedAsset{
				Ticker: ticker, Type: candidate.Type, Score: formatDecimal(score, 4), ScoreMetric: "DIVIDEND_YIELD_TTM",
			},
			score: score,
		}, true, nil
	default:
		return scoredCandidate{}, false, fmt.Errorf("unsupported asset type %q", candidate.Type)
	}
}

func compareScores(left, right scoredCandidate) int {
	switch {
	case left.isReference && right.isReference:
		return left.growth.Cmp(right.growth)
	case left.isReference:
		return left.growth.Cmp(cubeOfOnePlus(right.score))
	case right.isReference:
		return cubeOfOnePlus(left.score).Cmp(right.growth)
	default:
		return left.score.Cmp(right.score)
	}
}

func cubeOfOnePlus(value *big.Rat) *big.Rat {
	growth := new(big.Rat).Add(big.NewRat(1, 1), value)
	result := new(big.Rat).Mul(growth, growth)
	return result.Mul(result, growth)
}

func parseDecimal(value string) (*big.Rat, error) {
	if !decimalPattern.MatchString(value) {
		return nil, fmt.Errorf("%q is not a decimal string without exponent notation", value)
	}
	number, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, fmt.Errorf("%q is not a valid decimal number", value)
	}
	return number, nil
}

func formatDecimal(value *big.Rat, places int) string {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)
	numerator := new(big.Int).Mul(value.Num(), scale)
	denominator := value.Denom()
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(denominator) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}

	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(quotient, scale, fraction)
	return fmt.Sprintf("%s.%0*s", whole.String(), places, fraction.String())
}

func roundedCubeRootMinusOne(value *big.Rat) string {
	scale := big.NewInt(10_000)
	low := big.NewInt(0)
	high := big.NewInt(20_000)
	for compareScaledCube(high, scale, value) <= 0 {
		high.Mul(high, big.NewInt(2))
	}

	for new(big.Int).Sub(high, low).Cmp(big.NewInt(1)) > 0 {
		midpoint := new(big.Int).Add(low, high)
		midpoint.Rsh(midpoint, 1)
		if compareScaledCube(midpoint, scale, value) <= 0 {
			low.Set(midpoint)
		} else {
			high.Set(midpoint)
		}
	}

	thresholdNumerator := new(big.Int).Add(new(big.Int).Lsh(low, 1), big.NewInt(1))
	thresholdDenominator := new(big.Int).Lsh(scale, 1)
	threshold := new(big.Rat).SetFrac(thresholdNumerator, thresholdDenominator)
	if cube(threshold).Cmp(value) <= 0 {
		low.Add(low, big.NewInt(1))
	}
	low.Sub(low, scale)
	return formatScaledInteger(low, 4)
}

func compareScaledCube(value, scale *big.Int, target *big.Rat) int {
	ratio := new(big.Rat).SetFrac(new(big.Int).Set(value), new(big.Int).Set(scale))
	return cube(ratio).Cmp(target)
}

func cube(value *big.Rat) *big.Rat {
	square := new(big.Rat).Mul(value, value)
	return square.Mul(square, value)
}

func formatScaledInteger(value *big.Int, places int) string {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)
	negative := value.Sign() < 0
	absolute := new(big.Int).Abs(new(big.Int).Set(value))
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(absolute, scale, fraction)
	sign := ""
	if negative {
		sign = "-"
	}
	return fmt.Sprintf("%s%s.%0*s", sign, whole.String(), places, fraction.String())
}
