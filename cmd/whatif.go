package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bangarangler/burnrate/internal/pricing"
	"github.com/bangarangler/burnrate/internal/storage"
	"github.com/bangarangler/burnrate/internal/tracker"
	"github.com/spf13/cobra"
)

var whatIfCmd = &cobra.Command{
	Use:   "whatif [model]",
	Short: "Compare current session cost with another model",
	Long: `Calculates what your current session would have cost if you had used
a different model for all requests.

Examples:
  burnrate whatif gpt-4
  burnrate whatif claude-3-opus
  burnrate whatif (shows comparison with top models)`,
	Run: func(cmd *cobra.Command, args []string) {
		// Initialize DB first!
		if err := storage.InitDB(); err != nil {
			fmt.Printf("Error initializing DB: %v\n", err)
			return
		}

		// Ensure pricing is loaded
		pricing.UpdatePricing()

		// Get current session totals
		// NOTE: Since CLI is a separate process from dashboard, "current session" refers to
		// persistent session state or recent history.
		// For now, let's use Today's usage from DB as the "Session" for what-if analysis
		// if we aren't running inside the persistent dashboard.

		usages, _, err := tracker.Global.GetHistoricalUsage("today")
		if err != nil || len(usages) == 0 {
			fmt.Println("No recent usage data available. Start some work first!")
			return
		}

		var totalPrompt, totalCompletion int
		var currentCost float64
		for _, u := range usages {
			totalPrompt += u.PromptTokens
			totalCompletion += u.CompletionTokens
			currentCost += u.Cost
		}

		if len(args) > 0 {
			// Compare with specific model
			targetModel := args[0]
			hypotheticalCost, err := pricing.CalculateHypotheticalCost(targetModel, totalPrompt, totalCompletion)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				return
			}
			printComparison(currentCost, hypotheticalCost, targetModel)
		} else {
			// Show comparison table with common models
			commonModels := []string{
				"gpt-4o",
				"gpt-4o-mini",
				"claude-3-5-sonnet-latest",
				"claude-3-opus-20240229",
				"gemini-1.5-pro",
				"deepseek-coder",
			}

			fmt.Printf("Current Session Cost: $%.4f\n", currentCost)
			fmt.Println(strings.Repeat("-", 50))
			fmt.Printf("%-30s | %-10s | %s\n", "Model", "Cost", "Diff")
			fmt.Println(strings.Repeat("-", 50))

			// Sort models by cost for better readability? No, stick to list order or sort by cost diff.
			// Let's compute all first.
			type result struct {
				model string
				cost  float64
			}
			var results []result

			for _, m := range commonModels {
				cost, err := pricing.CalculateHypotheticalCost(m, totalPrompt, totalCompletion)
				if err == nil {
					results = append(results, result{m, cost})
				}
			}

			// Sort by cost ascending
			sort.Slice(results, func(i, j int) bool {
				return results[i].cost < results[j].cost
			})

			for _, res := range results {
				diff := res.cost - currentCost
				diffStr := fmt.Sprintf("+$%.4f", diff)
				if diff < 0 {
					diffStr = fmt.Sprintf("-$%.4f", -diff)
				} else if diff == 0 {
					diffStr = "="
				}

				fmt.Printf("%-30s | $%.4f  | %s\n", res.model, res.cost, diffStr)
			}
		}
	},
}

func printComparison(current, hypothetical float64, model string) {
	fmt.Printf("Current Cost:       $%.4f\n", current)
	fmt.Printf("Hypothetical Cost:  $%.4f (%s)\n", hypothetical, model)

	diff := hypothetical - current
	if diff > 0 {
		fmt.Printf("Difference:         +$%.4f (%.1fx more expensive)\n", diff, hypothetical/current)
	} else if diff < 0 {
		fmt.Printf("Savings:            $%.4f (%.1fx cheaper)\n", -diff, current/hypothetical)
	} else {
		fmt.Println("Difference:         None")
	}
}

func init() {
	rootCmd.AddCommand(whatIfCmd)
}
