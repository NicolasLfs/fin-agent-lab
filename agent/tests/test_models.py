import pytest
from pydantic import ValidationError

from fin_agent.models import RunRequest, RunResponse


def _request(**overrides):
    payload = {
        "reportId": "0192f298-345e-7e38-af88-e43f851a819d",
        "deterministicContext": {
            "fixedIncomeRates": [],
            "stocksAndFiis": [{"ticker": "VALE3", "type": "STOCK", "referencePrice": "68.50"}],
        },
        "rankedAssets": [
            {
                "rank": 1,
                "ticker": "VALE3",
                "type": "STOCK",
                "score": "0.1000",
                "scoreMetric": "EARNINGS_YIELD",
            }
        ],
        "assetTypes": ["STOCK"],
    }
    payload.update(overrides)
    return payload


def test_request_rejects_context_for_asset_type_not_requested():
    with pytest.raises(ValidationError, match="assetTypes"):
        RunRequest.model_validate(
            _request(
                deterministicContext={
                    "fixedIncomeRates": [],
                    "stocksAndFiis": [{"ticker": "HGLG11", "type": "FII", "referencePrice": "150.00"}],
                }
                ,rankedAssets=[
                    {
                        "rank": 1,
                        "ticker": "HGLG11",
                        "type": "FII",
                        "score": "0.0500",
                        "scoreMetric": "DIVIDEND_YIELD_TTM",
                    }
                ]
            )
        )


def test_request_requires_contiguous_go_calculated_ranking_with_decimal_scores():
    request = RunRequest.model_validate(
        _request(
            rankedAssets=[
                {
                    "rank": 1,
                    "ticker": "VALE3",
                    "type": "STOCK",
                    "score": "0.1000",
                    "scoreMetric": "EARNINGS_YIELD",
                }
            ]
        )
    )
    assert request.ranked_assets[0].ticker == "VALE3"
    assert request.ranked_assets[0].score == "0.1000"


def test_request_rejects_duplicate_assets_in_go_calculated_ranking():
    with pytest.raises(ValidationError, match="duplicate"):
        RunRequest.model_validate(
            _request(
                rankedAssets=[
                    {
                        "rank": 1,
                        "ticker": "VALE3",
                        "type": "STOCK",
                        "score": "0.1000",
                        "scoreMetric": "EARNINGS_YIELD",
                    },
                    {
                        "rank": 2,
                        "ticker": "VALE3",
                        "type": "STOCK",
                        "score": "0.1000",
                        "scoreMetric": "EARNINGS_YIELD",
                    },
                ]
            )
        )


def test_request_rejects_negative_go_calculated_score():
    with pytest.raises(ValidationError, match="nonnegative"):
        RunRequest.model_validate(
            _request(
                rankedAssets=[
                    {
                        "rank": 1,
                        "ticker": "VALE3",
                        "type": "STOCK",
                        "score": "-0.1000",
                        "scoreMetric": "EARNINGS_YIELD",
                    }
                ]
            )
        )


def test_request_rejects_financial_values_that_are_not_decimal_strings():
    with pytest.raises(ValidationError):
        RunRequest.model_validate(
            _request(
                deterministicContext={
                    "fixedIncomeRates": [
                        {
                            "ticker": "SELIC_SGS_432",
                            "basis": "REFERENCE_INDEX",
                            "source": "BCB_SGS:432",
                            "grossRate": 0.065,
                            "netRate": "0.0558",
                        }
                    ],
                    "stocksAndFiis": [],
                },
                assetTypes=["FIXED_INCOME"],
            )
        )


def test_response_rejects_noncontiguous_ranks_and_empty_citation_ids():
    with pytest.raises(ValidationError):
        RunResponse.model_validate(
            {
                "status": "COMPLETED",
                "rankedAssets": [
                    {
                        "rank": 2,
                        "ticker": "VALE3",
                        "type": "STOCK",
                        "score": "0.1000",
                        "scoreMetric": "EARNINGS_YIELD",
                    }
                ],
                "justificationText": "Grounded.",
                "citedNewsIds": [""],
            }
        )
