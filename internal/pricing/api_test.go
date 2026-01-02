package pricing

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchPricing(t *testing.T) {
	// Mock server response
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{
			"data": [
				{
					"id": "mock/gpt-new",
					"pricing": {
						"prompt": "0.000001", 
						"completion": "0.000002"
					}
				},
				{
					"id": "mock/existing-model",
					"pricing": {
						"prompt": "0.000005",
						"completion": "0.000010"
					}
				}
			]
		}`)
	}))
	defer ts.Close()

	// Override the API URL for testing
	originalURL := PricingAPIURL
	PricingAPIURL = ts.URL
	defer func() { PricingAPIURL = originalURL }()

	// Clear existing cache to force fetch
	// Note: We need to ensure thread safety if we do this in production code
	// For this test, we assume single-threaded execution context

	// 1. Fetch pricing
	err := UpdatePricing()
	if err != nil {
		t.Fatalf("UpdatePricing failed: %v", err)
	}

	// 2. Verify new model added
	p, ok := ModelPricing["mock/gpt-new"]
	if !ok {
		t.Error("New model 'mock/gpt-new' not found in pricing map")
	}

	// Check values (0.000001 * 1M = 1.0, 0.000002 * 1M = 2.0)
	if p.Input != 1.0 || p.Output != 2.0 {
		t.Errorf("Expected 1.0/2.0, got %f/%f", p.Input, p.Output)
	}

	// 3. Verify fallback still exists (assuming 'gpt-4o' is in the hardcoded list)
	_, ok = ModelPricing["gpt-4o"]
	if !ok {
		t.Error("Hardcoded model 'gpt-4o' disappeared")
	}
}
