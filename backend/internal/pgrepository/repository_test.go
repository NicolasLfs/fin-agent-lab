package pgrepository

import (
	"testing"

	"fin-agent/internal/httpapi"
)

func TestRequestHashIsIndependentOfAssetTypeOrder(t *testing.T) {
	first, err := requestHash(httpapi.GenerateRequest{AssetTypes: []httpapi.AssetType{httpapi.AssetTypeStock, httpapi.AssetTypeFII}})
	if err != nil {
		t.Fatalf("requestHash() error = %v", err)
	}
	second, err := requestHash(httpapi.GenerateRequest{AssetTypes: []httpapi.AssetType{httpapi.AssetTypeFII, httpapi.AssetTypeStock}})
	if err != nil {
		t.Fatalf("requestHash() error = %v", err)
	}
	if first != second {
		t.Fatalf("request hashes differ for equivalent requests: %q != %q", first, second)
	}
}

func TestRequestHashChangesWhenSelectedTypesChange(t *testing.T) {
	first, err := requestHash(httpapi.GenerateRequest{AssetTypes: []httpapi.AssetType{httpapi.AssetTypeStock}})
	if err != nil {
		t.Fatalf("requestHash() error = %v", err)
	}
	second, err := requestHash(httpapi.GenerateRequest{AssetTypes: []httpapi.AssetType{httpapi.AssetTypeFII}})
	if err != nil {
		t.Fatalf("requestHash() error = %v", err)
	}
	if first == second {
		t.Fatal("requestHash() returned equal values for different requests")
	}
}
