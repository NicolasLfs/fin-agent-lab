package recommendation

import (
	"context"
	"errors"
	"testing"

	"fin-agent/internal/agentclient"
	"fin-agent/internal/httpapi"
)

type repositoryFunc func(context.Context, string, httpapi.GenerateRequest, func(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error)) (httpapi.GenerateResponse, error)

func (f repositoryFunc) Execute(ctx context.Context, key string, request httpapi.GenerateRequest, invoke func(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error)) (httpapi.GenerateResponse, error) {
	return f(ctx, key, request, invoke)
}

type agentFunc func(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error)

func (f agentFunc) Run(ctx context.Context, request agentclient.RunRequest) (agentclient.RunResponse, error) {
	return f(ctx, request)
}

func TestGenerateValidatesAgentRankingAgainstDeterministicContext(t *testing.T) {
	repo := repositoryFunc(func(ctx context.Context, key string, request httpapi.GenerateRequest, invoke func(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error)) (httpapi.GenerateResponse, error) {
		result, err := invoke(ctx, agentclient.RunRequest{
			ReportID: "report-1",
			DeterministicContext: agentclient.DeterministicContext{
				StocksAndFiis: []agentclient.StockOrFII{{Ticker: "VALE3", Type: "STOCK", ReferencePrice: "68.50"}},
			},
			RankedAssets: []agentclient.RankedAsset{
				{Rank: 1, Ticker: "VALE3", Type: "STOCK", Score: "0.1000", ScoreMetric: "EARNINGS_YIELD"},
			},
			AssetTypes: []string{"STOCK"},
		})
		if err != nil {
			return httpapi.GenerateResponse{}, err
		}
		if result.RankedAssets[0].Ticker != "VALE3" {
			t.Errorf("agent result = %#v", result)
		}
		return httpapi.GenerateResponse{ReportID: "report-1", Status: "COMPLETED"}, nil
	})
	service := New(repo, agentFunc(func(_ context.Context, request agentclient.RunRequest) (agentclient.RunResponse, error) {
		if request.ReportID != "report-1" || len(request.AssetTypes) != 1 {
			t.Errorf("agent request = %#v", request)
		}
		return agentclient.RunResponse{
			Status: "COMPLETED",
			RankedAssets: []agentclient.RankedAsset{
				{Rank: 1, Ticker: "VALE3", Type: "STOCK", Score: "0.1000", ScoreMetric: "EARNINGS_YIELD"},
			},
			JustificationText: "Based on the supplied context.",
			CitedNewsIDs:      []string{"point-1"},
		}, nil
	}))

	_, err := service.Generate(context.Background(), "key-1", httpapi.GenerateRequest{AssetTypes: []httpapi.AssetType{httpapi.AssetTypeStock}})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
}

func TestGenerateRejectsAgentInventedAssetAndInvalidRanks(t *testing.T) {
	tests := []struct {
		name string
		rank []agentclient.RankedAsset
	}{
		{name: "unknown ticker", rank: []agentclient.RankedAsset{{Rank: 1, Ticker: "UNKNOWN", Type: "STOCK"}}},
		{name: "wrong rank", rank: []agentclient.RankedAsset{{Rank: 2, Ticker: "VALE3", Type: "STOCK"}}},
		{name: "wrong type", rank: []agentclient.RankedAsset{{Rank: 1, Ticker: "VALE3", Type: "FII"}}},
		{name: "duplicate ticker", rank: []agentclient.RankedAsset{{Rank: 1, Ticker: "VALE3", Type: "STOCK"}, {Rank: 2, Ticker: "VALE3", Type: "STOCK"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := repositoryFunc(func(ctx context.Context, _ string, _ httpapi.GenerateRequest, invoke func(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error)) (httpapi.GenerateResponse, error) {
				_, err := invoke(ctx, agentclient.RunRequest{
					ReportID: "report-1",
					DeterministicContext: agentclient.DeterministicContext{
						StocksAndFiis: []agentclient.StockOrFII{{Ticker: "VALE3", Type: "STOCK", ReferencePrice: "68.50"}},
					},
					RankedAssets: []agentclient.RankedAsset{
						{Rank: 1, Ticker: "VALE3", Type: "STOCK", Score: "0.1000", ScoreMetric: "EARNINGS_YIELD"},
					},
					AssetTypes: []string{"STOCK"},
				})
				return httpapi.GenerateResponse{}, err
			})
			service := New(repo, agentFunc(func(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error) {
				return agentclient.RunResponse{Status: "COMPLETED", RankedAssets: tt.rank, JustificationText: "Explanation."}, nil
			}))

			_, err := service.Generate(context.Background(), "key-1", httpapi.GenerateRequest{AssetTypes: []httpapi.AssetType{httpapi.AssetTypeStock}})
			if err == nil {
				t.Fatal("Generate() error = nil, want agent response validation error")
			}
		})
	}
}

func TestGenerateRejectsAgentChangingGoCalculatedRanking(t *testing.T) {
	payload := agentclient.RunRequest{
		ReportID: "report-1",
		DeterministicContext: agentclient.DeterministicContext{
			StocksAndFiis: []agentclient.StockOrFII{
				{Ticker: "VALE3", Type: "STOCK", ReferencePrice: "68.50"},
				{Ticker: "HGLG11", Type: "FII", ReferencePrice: "150.00"},
			},
		},
		RankedAssets: []agentclient.RankedAsset{
			{Rank: 1, Ticker: "VALE3", Type: "STOCK", Score: "0.1000", ScoreMetric: "EARNINGS_YIELD"},
			{Rank: 2, Ticker: "HGLG11", Type: "FII", Score: "0.0500", ScoreMetric: "DIVIDEND_YIELD_TTM"},
		},
	}
	repo := repositoryFunc(func(ctx context.Context, _ string, request httpapi.GenerateRequest, invoke func(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error)) (httpapi.GenerateResponse, error) {
		_, err := invoke(ctx, payload)
		return httpapi.GenerateResponse{}, err
	})
	service := New(repo, agentFunc(func(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error) {
		return agentclient.RunResponse{
			Status: "COMPLETED",
			RankedAssets: []agentclient.RankedAsset{
				{Rank: 1, Ticker: "HGLG11", Type: "FII", Score: "0.0500", ScoreMetric: "DIVIDEND_YIELD_TTM"},
				{Rank: 2, Ticker: "VALE3", Type: "STOCK", Score: "0.1000", ScoreMetric: "EARNINGS_YIELD"},
			},
			JustificationText: "Grounded analysis.",
		}, nil
	}))

	_, err := service.Generate(context.Background(), "key-1", httpapi.GenerateRequest{
		AssetTypes: []httpapi.AssetType{httpapi.AssetTypeStock, httpapi.AssetTypeFII},
	})
	if err == nil {
		t.Fatal("Generate() error = nil, want rejection of changed deterministic ranking")
	}
}

func TestGeneratePropagatesAgentFailures(t *testing.T) {
	wantErr := errors.New("agent transport failed")
	repo := repositoryFunc(func(ctx context.Context, _ string, _ httpapi.GenerateRequest, invoke func(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error)) (httpapi.GenerateResponse, error) {
		_, err := invoke(ctx, agentclient.RunRequest{
			ReportID: "report-1",
			DeterministicContext: agentclient.DeterministicContext{
				StocksAndFiis: []agentclient.StockOrFII{{Ticker: "VALE3", Type: "STOCK", ReferencePrice: "68.50"}},
			},
			RankedAssets: []agentclient.RankedAsset{
				{Rank: 1, Ticker: "VALE3", Type: "STOCK", Score: "0.1000", ScoreMetric: "EARNINGS_YIELD"},
			},
		})
		return httpapi.GenerateResponse{}, err
	})
	service := New(repo, agentFunc(func(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error) {
		return agentclient.RunResponse{}, wantErr
	}))

	_, err := service.Generate(context.Background(), "key-1", httpapi.GenerateRequest{AssetTypes: []httpapi.AssetType{httpapi.AssetTypeStock}})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Generate() error = %v, want %v", err, wantErr)
	}
}
