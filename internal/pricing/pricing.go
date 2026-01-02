// internal/pricing/pricing.go
package pricing

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Prices per 1M tokens (input / output) - latest as of Dec 2025
var ModelPricing = map[string]struct {
	Input    float64
	Output   float64
	Provider string // For display
}{
	// OpenAI
	"gpt-5":         {2.00, 10.00, "OpenAI"},
	"gpt-5.2":       {1.75, 14.00, "OpenAI"},
	"gpt-4o":        {2.50, 10.00, "OpenAI"},
	"gpt-4o-mini":   {0.15, 0.60, "OpenAI"},
	"gpt-4-turbo":   {10.00, 30.00, "OpenAI"},
	"gpt-4":         {30.00, 60.00, "OpenAI"},
	"gpt-3.5-turbo": {0.50, 1.50, "OpenAI"},
	"o1":            {15.00, 60.00, "OpenAI"},
	"o1-preview":    {15.00, 60.00, "OpenAI"},
	"o1-mini":       {3.00, 12.00, "OpenAI"},
	"o3-mini":       {1.10, 4.40, "OpenAI"},

	// Anthropic Claude (various naming conventions)
	"claude-opus-4.5":             {5.00, 25.00, "Anthropic"},
	"claude-sonnet-4.5":           {3.00, 15.00, "Anthropic"},
	"claude-haiku-4":              {0.25, 1.25, "Anthropic"},
	"claude-3-5-sonnet-20241022":  {3.00, 15.00, "Anthropic"},
	"claude-3-5-sonnet-latest":    {3.00, 15.00, "Anthropic"},
	"claude-3-opus-20240229":      {15.00, 75.00, "Anthropic"},
	"claude-3-sonnet-20240229":    {3.00, 15.00, "Anthropic"},
	"claude-3-haiku-20240307":     {0.25, 1.25, "Anthropic"},
	"anthropic/claude-3-5-sonnet": {3.00, 15.00, "Anthropic"},
	"anthropic/claude-sonnet-4":   {3.00, 15.00, "Anthropic"},

	// Groq (very low cost, uses OpenAI format)
	"llama-3.1-405b": {0.59, 0.79, "Groq"},
	"llama-3.1-70b":  {0.59, 0.79, "Groq"},
	"mixtral-8x22b":  {0.27, 0.27, "Groq"},

	// xAI Grok
	"grok-4.1": {0.20, 0.50, "xAI Grok"},
	"grok-4":   {6.00, 30.00, "xAI Grok"},

	// Gemini (Google) - various naming conventions from Aider
	"gemini-2.5-pro":          {4.00, 20.00, "Google Gemini"},
	"gemini-2.5-flash":        {0.30, 2.50, "Google Gemini"},
	"gemini/gemini-2.5-pro":   {4.00, 20.00, "Google Gemini"},
	"gemini/gemini-2.5-flash": {0.30, 2.50, "Google Gemini"},
	"gemini-1.5-pro":          {3.50, 10.50, "Google Gemini"},
	"gemini-1.5-flash":        {0.075, 0.30, "Google Gemini"},
	"gemini/gemini-1.5-pro":   {3.50, 10.50, "Google Gemini"},
	"gemini/gemini-1.5-flash": {0.075, 0.30, "Google Gemini"},

	// DeepSeek
	"deepseek-chat":          {0.14, 0.28, "DeepSeek"},
	"deepseek-coder":         {0.14, 0.28, "DeepSeek"},
	"deepseek/deepseek-chat": {0.14, 0.28, "DeepSeek"},

	// Azure OpenAI / Copilot (same as OpenAI pricing)
	// Just use the same model names as OpenAI
}

// PricingAPIURL is the endpoint for fetching model pricing
var PricingAPIURL = "https://openrouter.ai/api/v1/models"

var (
	lastFetchTime time.Time
	fetchMutex    sync.Mutex
	cacheDuration = 1 * time.Hour
)

type openRouterResponse struct {
	Data []struct {
		ID      string `json:"id"`
		Pricing struct {
			Prompt     string `json:"prompt"`
			Completion string `json:"completion"`
		} `json:"pricing"`
		Name string `json:"name"`
	} `json:"data"`
}

// UpdatePricing fetches the latest pricing from the API
func UpdatePricing() error {
	fetchMutex.Lock()
	defer fetchMutex.Unlock()

	// Rate limit checks (simple time-based cache)
	if time.Since(lastFetchTime) < cacheDuration {
		return nil
	}

	resp, err := http.Get(PricingAPIURL)
	if err != nil {
		return fmt.Errorf("failed to fetch pricing: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	var data openRouterResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("failed to decode pricing data: %w", err)
	}

	for _, model := range data.Data {
		// OpenRouter pricing is per token, we store per 1M tokens
		inputPrice, err := strconv.ParseFloat(model.Pricing.Prompt, 64)
		if err != nil {
			continue
		}
		outputPrice, err := strconv.ParseFloat(model.Pricing.Completion, 64)
		if err != nil {
			continue
		}

		// Convert to per 1M tokens
		inputPerM := inputPrice * 1_000_000
		outputPerM := outputPrice * 1_000_000

		// Determine provider from ID or Name
		provider := "Unknown"
		if strings.Contains(model.ID, "/") {
			parts := strings.Split(model.ID, "/")
			provider = parts[0]
		} else if strings.Contains(model.Name, ":") {
			parts := strings.Split(model.Name, ":")
			provider = parts[0]
		}

		ModelPricing[model.ID] = struct {
			Input    float64
			Output   float64
			Provider string
		}{
			Input:    inputPerM,
			Output:   outputPerM,
			Provider: provider,
		}
	}

	lastFetchTime = time.Now()
	return nil
}

// GetLastFetchTime returns the time of the last successful API fetch
func GetLastFetchTime() time.Time {
	fetchMutex.Lock()
	defer fetchMutex.Unlock()
	return lastFetchTime
}

func CalculateCost(model string, promptTokens, completionTokens int) float64 {
	// Handle free models (OpenRouter :free suffix, etc.)
	if strings.HasSuffix(model, ":free") || strings.Contains(model, ":free ") {
		return 0.0
	}

	p, ok := ModelPricing[model]
	if !ok {
		// Fallback to cheapest safe model
		p = ModelPricing["gpt-4o-mini"]
	}

	inputCost := float64(promptTokens) / 1_000_000 * p.Input
	outputCost := float64(completionTokens) / 1_000_000 * p.Output

	return inputCost + outputCost
}

// CalculateHypotheticalCost calculates what the cost would have been with a different model
func CalculateHypotheticalCost(targetModel string, promptTokens, completionTokens int) (float64, error) {
	p, ok := ModelPricing[targetModel]
	if !ok {
		// Try fuzzy matching or common aliases
		for k, v := range ModelPricing {
			if strings.EqualFold(k, targetModel) || strings.Contains(strings.ToLower(k), strings.ToLower(targetModel)) {
				p = v
				ok = true
				break
			}
		}
		if !ok {
			return 0, fmt.Errorf("model %s not found", targetModel)
		}
	}

	inputCost := float64(promptTokens) / 1_000_000 * p.Input
	outputCost := float64(completionTokens) / 1_000_000 * p.Output

	return inputCost + outputCost, nil
}

// GetAvailableModels returns a list of model IDs available for comparison
func GetAvailableModels() []string {
	var models []string
	for k := range ModelPricing {
		models = append(models, k)
	}
	return models
}

// CommonModels lists popular models for quick comparison
var CommonModels = []string{
	"gpt-4o",
	"gpt-4o-mini",
	"claude-3-5-sonnet-latest",
	"claude-3-opus-20240229",
	"gemini-1.5-pro",
	"deepseek-coder",
}
