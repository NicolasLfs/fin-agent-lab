import re
from decimal import Decimal, InvalidOperation
from typing import Literal
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field, StrictStr, field_validator, model_validator


AssetType = Literal["FIXED_INCOME", "STOCK", "FII"]
_TICKER = re.compile(r"^[A-Z0-9][A-Z0-9._-]{0,31}$")
_DECIMAL = re.compile(r"^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$")


def _validate_decimal_string(value: str, *, positive: bool = False) -> str:
    if not _DECIMAL.fullmatch(value):
        raise ValueError("must be a finite decimal string without exponent notation")
    try:
        decimal = Decimal(value)
    except InvalidOperation as error:
        raise ValueError("must be a valid decimal string") from error
    if not decimal.is_finite() or (positive and decimal <= 0):
        raise ValueError("must be finite and positive" if positive else "must be finite")
    return value


class ContractModel(BaseModel):
    model_config = ConfigDict(extra="forbid", populate_by_name=True)


class FixedIncomeRate(ContractModel):
    ticker: StrictStr
    basis: Literal["REFERENCE_INDEX"]
    source: StrictStr = Field(min_length=1, max_length=128)
    gross_rate: StrictStr = Field(alias="grossRate", min_length=1, max_length=64)
    net_rate: StrictStr = Field(alias="netRate", min_length=1, max_length=64)

    @field_validator("ticker")
    @classmethod
    def validate_ticker(cls, value: str) -> str:
        if not _TICKER.fullmatch(value):
            raise ValueError("ticker has an invalid format")
        return value

    @field_validator("net_rate")
    @classmethod
    def validate_net_rate(cls, value: str) -> str:
        return _validate_decimal_string(value)

    @field_validator("gross_rate")
    @classmethod
    def validate_gross_rate(cls, value: str) -> str:
        return _validate_decimal_string(value)


class StockOrFII(ContractModel):
    ticker: StrictStr
    type: Literal["STOCK", "FII"]
    reference_price: StrictStr = Field(alias="referencePrice", min_length=1, max_length=64)

    @field_validator("ticker")
    @classmethod
    def validate_ticker(cls, value: str) -> str:
        if not _TICKER.fullmatch(value):
            raise ValueError("ticker has an invalid format")
        return value

    @field_validator("reference_price")
    @classmethod
    def validate_reference_price(cls, value: str) -> str:
        return _validate_decimal_string(value, positive=True)


class DeterministicContext(ContractModel):
    fixed_income_rates: list[FixedIncomeRate] = Field(alias="fixedIncomeRates", max_length=500)
    stocks_and_fiis: list[StockOrFII] = Field(alias="stocksAndFiis", max_length=500)


class RunRequest(ContractModel):
    report_id: UUID = Field(alias="reportId")
    deterministic_context: DeterministicContext = Field(alias="deterministicContext")
    ranked_assets: list["RankedAsset"] = Field(alias="rankedAssets", min_length=1, max_length=500)
    asset_types: list[AssetType] = Field(alias="assetTypes", min_length=1, max_length=3)

    @model_validator(mode="after")
    def validate_context(self) -> "RunRequest":
        tickers = [item.ticker for item in self.deterministic_context.fixed_income_rates]
        tickers.extend(item.ticker for item in self.deterministic_context.stocks_and_fiis)
        if len(tickers) != len(set(tickers)):
            raise ValueError("deterministic context contains duplicate tickers")
        fixed_income_tickers = {
            rate.ticker for rate in self.deterministic_context.fixed_income_rates
        }
        if any(item.ticker in fixed_income_tickers for item in self.deterministic_context.stocks_and_fiis):
            raise ValueError("ticker cannot be both fixed income and a stock or FII")
        if len(self.asset_types) != len(set(self.asset_types)):
            raise ValueError("assetTypes must not contain duplicates")
        requested = set(self.asset_types)
        if self.deterministic_context.fixed_income_rates and "FIXED_INCOME" not in requested:
            raise ValueError("deterministic context contains an asset type not requested in assetTypes")
        if any(item.type not in requested for item in self.deterministic_context.stocks_and_fiis):
            raise ValueError("deterministic context contains an asset type not requested in assetTypes")
        allowed_types = {
            rate.ticker: "FIXED_INCOME"
            for rate in self.deterministic_context.fixed_income_rates
        }
        allowed_types.update({
            item.ticker: item.type for item in self.deterministic_context.stocks_and_fiis
        })
        if [asset.rank for asset in self.ranked_assets] != list(range(1, len(self.ranked_assets) + 1)):
            raise ValueError("rankedAssets must have contiguous ranks starting at 1")
        ranked_tickers = [asset.ticker for asset in self.ranked_assets]
        if len(ranked_tickers) != len(set(ranked_tickers)):
            raise ValueError("rankedAssets contains duplicate tickers")
        for asset in self.ranked_assets:
            if allowed_types.get(asset.ticker) != asset.type:
                raise ValueError("rankedAssets must contain only assets from deterministicContext")
        return self


class RankedAsset(ContractModel):
    rank: int = Field(ge=1)
    ticker: StrictStr = Field(min_length=1, max_length=32)
    type: AssetType
    score: StrictStr
    score_metric: Literal[
        "EARNINGS_YIELD",
        "DIVIDEND_YIELD_TTM",
        "REFERENCE_INDEX_NET_RATE",
    ] = Field(alias="scoreMetric")

    @field_validator("ticker")
    @classmethod
    def validate_ticker(cls, value: str) -> str:
        if not _TICKER.fullmatch(value):
            raise ValueError("ticker has an invalid format")
        return value

    @field_validator("score")
    @classmethod
    def validate_score(cls, value: str) -> str:
        if Decimal(_validate_decimal_string(value)) < 0:
            raise ValueError("score must be nonnegative")
        return value

    @model_validator(mode="after")
    def validate_metric_type(self) -> "RankedAsset":
        expected = {
            "STOCK": "EARNINGS_YIELD",
            "FII": "DIVIDEND_YIELD_TTM",
            "FIXED_INCOME": "REFERENCE_INDEX_NET_RATE",
        }[self.type]
        if self.score_metric != expected:
            raise ValueError("scoreMetric does not match the ranked asset type")
        return self


class RunResponse(ContractModel):
    status: Literal["COMPLETED"]
    ranked_assets: list[RankedAsset] = Field(alias="rankedAssets", min_length=1, max_length=500)
    justification_text: StrictStr = Field(alias="justificationText", min_length=1, max_length=8000)
    cited_news_ids: list[StrictStr] = Field(alias="citedNewsIds", max_length=500)

    @model_validator(mode="after")
    def validate_ranking(self) -> "RunResponse":
        if [asset.rank for asset in self.ranked_assets] != list(range(1, len(self.ranked_assets) + 1)):
            raise ValueError("ranked assets must have contiguous ranks starting at 1")
        tickers = [asset.ticker for asset in self.ranked_assets]
        if len(tickers) != len(set(tickers)):
            raise ValueError("ranked assets contain duplicate tickers")
        if any(not news_id.strip() for news_id in self.cited_news_ids):
            raise ValueError("cited news IDs cannot be empty")
        if len(self.cited_news_ids) != len(set(self.cited_news_ids)):
            raise ValueError("cited news IDs must be unique")
        return self
