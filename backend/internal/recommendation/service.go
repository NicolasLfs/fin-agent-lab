package recommendation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"fin-agent/internal/agentclient"
	"fin-agent/internal/httpapi"
)

var ErrNoMarketData = errors.New("no deterministic market data is available for the requested asset types")

type Repository interface {
	Execute(
		context.Context,
		string,
		httpapi.GenerateRequest,
		func(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error),
	) (httpapi.GenerateResponse, error)
}

type Agent interface {
	Run(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error)
}

type Service struct {
	repository Repository
	agent      Agent
}

func New(repository Repository, agent Agent) *Service {
	return &Service{repository: repository, agent: agent}
}

func (s *Service) Generate(ctx context.Context, key string, request httpapi.GenerateRequest) (httpapi.GenerateResponse, error) {
	return s.repository.Execute(ctx, key, request, func(ctx context.Context, payload agentclient.RunRequest) (agentclient.RunResponse, error) {
		if payload.ReportID == "" {
			return agentclient.RunResponse{}, errors.New("repository supplied an empty reportId")
		}
		if err := validateDeterministicContext(request, payload); err != nil {
			return agentclient.RunResponse{}, err
		}
		response, err := s.agent.Run(ctx, payload)
		if err != nil {
			return agentclient.RunResponse{}, err
		}
		if err := validateAgentResponse(request, payload, response); err != nil {
			return agentclient.RunResponse{}, err
		}
		return response, nil
	})
}

func validateDeterministicContext(request httpapi.GenerateRequest, payload agentclient.RunRequest) error {
	selected := make(map[httpapi.AssetType]struct{}, len(request.AssetTypes))
	for _, assetType := range request.AssetTypes {
		selected[assetType] = struct{}{}
	}
	available := false
	tickers := make(map[string]struct{})
	for _, rate := range payload.DeterministicContext.FixedIncomeRates {
		if _, requested := selected[httpapi.AssetTypeFixedIncome]; !requested {
			return errors.New("deterministic context contains an unrequested fixed-income asset")
		}
		if strings.TrimSpace(rate.Ticker) == "" || strings.TrimSpace(rate.NetRate) == "" {
			return errors.New("fixed-income context requires ticker and netRate")
		}
		if _, duplicate := tickers[rate.Ticker]; duplicate {
			return fmt.Errorf("deterministic context contains duplicate ticker %q", rate.Ticker)
		}
		tickers[rate.Ticker] = struct{}{}
		available = true
	}
	for _, asset := range payload.DeterministicContext.StocksAndFiis {
		assetType := httpapi.AssetType(asset.Type)
		if assetType != httpapi.AssetTypeStock && assetType != httpapi.AssetTypeFII {
			return fmt.Errorf("unsupported asset type %q in deterministic context", asset.Type)
		}
		if _, requested := selected[assetType]; !requested {
			return fmt.Errorf("deterministic context contains unrequested asset type %q", asset.Type)
		}
		if strings.TrimSpace(asset.Ticker) == "" || strings.TrimSpace(asset.ReferencePrice) == "" {
			return errors.New("stock and FII context requires ticker and referencePrice")
		}
		if _, duplicate := tickers[asset.Ticker]; duplicate {
			return fmt.Errorf("deterministic context contains duplicate ticker %q", asset.Ticker)
		}
		tickers[asset.Ticker] = struct{}{}
		available = true
	}
	if !available {
		return ErrNoMarketData
	}
	return nil
}

func validateAgentResponse(request httpapi.GenerateRequest, payload agentclient.RunRequest, response agentclient.RunResponse) error {
	if response.Status != "COMPLETED" || response.JustificationText == "" {
		return errors.New("agent response must be COMPLETED and include a justification")
	}
	allowed := make(map[string]string)
	for _, rate := range payload.DeterministicContext.FixedIncomeRates {
		allowed[rate.Ticker] = string(httpapi.AssetTypeFixedIncome)
	}
	for _, asset := range payload.DeterministicContext.StocksAndFiis {
		allowed[asset.Ticker] = asset.Type
	}
	requested := make(map[string]struct{}, len(request.AssetTypes))
	for _, assetType := range request.AssetTypes {
		requested[string(assetType)] = struct{}{}
	}
	ranked := make(map[string]struct{}, len(response.RankedAssets))
	for index, asset := range response.RankedAssets {
		if asset.Rank != index+1 {
			return fmt.Errorf("agent ranking must use contiguous ranks starting at 1; position %d has rank %d", index+1, asset.Rank)
		}
		expectedType, exists := allowed[asset.Ticker]
		if !exists || expectedType != asset.Type {
			return fmt.Errorf("agent ranked asset %q with type %q outside the deterministic context", asset.Ticker, asset.Type)
		}
		if _, exists := requested[asset.Type]; !exists {
			return fmt.Errorf("agent ranked unrequested asset type %q", asset.Type)
		}
		if _, duplicate := ranked[asset.Ticker]; duplicate {
			return fmt.Errorf("agent ranking contains duplicate ticker %q", asset.Ticker)
		}
		ranked[asset.Ticker] = struct{}{}
	}
	cited := make(map[string]struct{}, len(response.CitedNewsIDs))
	for _, id := range response.CitedNewsIDs {
		if strings.TrimSpace(id) == "" {
			return errors.New("agent response contains an empty cited news ID")
		}
		if _, duplicate := cited[id]; duplicate {
			return fmt.Errorf("agent response contains duplicate cited news ID %q", id)
		}
		cited[id] = struct{}{}
	}
	return nil
}
