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
            )
        )


def test_request_rejects_financial_values_that_are_not_decimal_strings():
    with pytest.raises(ValidationError):
        RunRequest.model_validate(
            _request(
                deterministicContext={
                    "fixedIncomeRates": [{"ticker": "TESOURO_IPCA_2029", "netRate": 0.065}],
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
                "rankedAssets": [{"rank": 2, "ticker": "VALE3", "type": "STOCK"}],
                "justificationText": "Grounded.",
                "citedNewsIds": [""],
            }
        )
