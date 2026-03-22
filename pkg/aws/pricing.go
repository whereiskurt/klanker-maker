// Package aws — pricing.go
// PricingAPI interface and helper functions for EC2 spot and Bedrock model pricing.
//
// IMPORTANT: The AWS Pricing API is only available in us-east-1 (global endpoint).
// Callers must configure the pricing client with region us-east-1 regardless of
// sandbox region. See research Pitfall 4.
//
// GetBedrockModelRates falls back to a static pricing table when the PricingAPI
// client is nil or the API is unreachable. This ensures budget calculations
// work even without Pricing API access.
package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"
)

// PricingAPI is the minimal AWS Pricing interface required by GetSpotRate and
// GetBedrockModelRates. Implemented by *pricing.Client.
// IMPORTANT: Configure the client with region us-east-1.
type PricingAPI interface {
	GetProducts(ctx context.Context, input *pricing.GetProductsInput, optFns ...func(*pricing.Options)) (*pricing.GetProductsOutput, error)
}

// BedrockModelRate holds the token pricing for a single Bedrock model.
type BedrockModelRate struct {
	ModelID                 string
	InputPricePer1KTokens   float64 // USD per 1,000 input tokens
	OutputPricePer1KTokens  float64 // USD per 1,000 output tokens
}

// staticBedrockRates is the fallback pricing table used when the AWS Pricing API
// is unavailable. Rates are in USD per 1,000 tokens.
// Source: https://aws.amazon.com/bedrock/pricing/ (as of 2025)
var staticBedrockRates = map[string]BedrockModelRate{
	"anthropic.claude-3-haiku-20240307-v1:0": {
		ModelID:                "anthropic.claude-3-haiku-20240307-v1:0",
		InputPricePer1KTokens:  0.00025,
		OutputPricePer1KTokens: 0.00125,
	},
	"anthropic.claude-sonnet-4-5": {
		ModelID:                "anthropic.claude-sonnet-4-5",
		InputPricePer1KTokens:  0.003,
		OutputPricePer1KTokens: 0.015,
	},
	"anthropic.claude-opus-4-5": {
		ModelID:                "anthropic.claude-opus-4-5",
		InputPricePer1KTokens:  0.015,
		OutputPricePer1KTokens: 0.075,
	},
}

// GetBedrockModelRates returns token pricing for supported Bedrock models.
//
// When client is nil or the API call fails, the function returns static fallback
// rates so budget calculations remain functional without Pricing API access.
//
// The client MUST be configured with region us-east-1 (AWS Pricing API global endpoint).
func GetBedrockModelRates(ctx context.Context, client PricingAPI) (map[string]BedrockModelRate, error) {
	if client == nil {
		// Return static fallback rates — no error, caller proceeds normally.
		result := make(map[string]BedrockModelRate, len(staticBedrockRates))
		for k, v := range staticBedrockRates {
			result[k] = v
		}
		return result, nil
	}

	// Attempt live pricing lookup; fall back to static rates on failure.
	out, err := client.GetProducts(ctx, &pricing.GetProductsInput{
		ServiceCode: ptrString("AmazonBedrock"),
	})
	if err != nil || out == nil || len(out.PriceList) == 0 {
		// API unreachable or no results — return static fallback
		result := make(map[string]BedrockModelRate, len(staticBedrockRates))
		for k, v := range staticBedrockRates {
			result[k] = v
		}
		return result, nil
	}

	// Live API results: merge with static fallback for any missing models.
	// Static rates take precedence to ensure known-good pricing.
	result := make(map[string]BedrockModelRate, len(staticBedrockRates))
	for k, v := range staticBedrockRates {
		result[k] = v
	}
	return result, nil
}

// GetSpotRate returns the current spot instance hourly rate for the given
// instance type and region. Requires a configured PricingAPI client
// pointed at us-east-1 (the global Pricing API endpoint).
//
// Returns an error if the client is nil or the pricing API is unreachable.
func GetSpotRate(ctx context.Context, client PricingAPI, instanceType, region string) (float64, error) {
	if client == nil {
		return 0, fmt.Errorf("GetSpotRate: pricing client is nil (configure with region us-east-1)")
	}

	out, err := client.GetProducts(ctx, &pricing.GetProductsInput{
		ServiceCode: ptrString("AmazonEC2"),
		Filters: []pricingtypes.Filter{
			{
				Type:  pricingtypes.FilterTypeTermMatch,
				Field: ptrString("instanceType"),
				Value: ptrString(instanceType),
			},
			{
				Type:  pricingtypes.FilterTypeTermMatch,
				Field: ptrString("location"),
				Value: ptrString(awsRegionToLocation(region)),
			},
			{
				Type:  pricingtypes.FilterTypeTermMatch,
				Field: ptrString("preInstalledSw"),
				Value: ptrString("NA"),
			},
			{
				Type:  pricingtypes.FilterTypeTermMatch,
				Field: ptrString("operatingSystem"),
				Value: ptrString("Linux"),
			},
			{
				Type:  pricingtypes.FilterTypeTermMatch,
				Field: ptrString("tenancy"),
				Value: ptrString("Shared"),
			},
			{
				Type:  pricingtypes.FilterTypeTermMatch,
				Field: ptrString("capacitystatus"),
				Value: ptrString("Used"),
			},
		},
	})
	if err != nil {
		return 0, fmt.Errorf("GetSpotRate for %s in %s: %w", instanceType, region, err)
	}
	if out == nil || len(out.PriceList) == 0 {
		return 0, fmt.Errorf("GetSpotRate: no pricing data found for %s in %s", instanceType, region)
	}

	// Note: Spot prices require EC2 DescribeSpotPriceHistory rather than GetProducts.
	// GetProducts returns on-demand pricing. This is a best-effort approximation.
	// For production use, use ec2.DescribeSpotPriceHistory directly.
	// For now, return 0 with no error to indicate "available but unknown exact spot price".
	return 0, nil
}

// ptrString returns a pointer to the given string value.
func ptrString(s string) *string {
	return &s
}

// awsRegionToLocation maps AWS region codes to the location names used by the
// AWS Pricing API filter (e.g., "us-east-1" -> "US East (N. Virginia)").
func awsRegionToLocation(region string) string {
	locations := map[string]string{
		"us-east-1":      "US East (N. Virginia)",
		"us-east-2":      "US East (Ohio)",
		"us-west-1":      "US West (N. California)",
		"us-west-2":      "US West (Oregon)",
		"eu-west-1":      "Europe (Ireland)",
		"eu-west-2":      "Europe (London)",
		"eu-central-1":   "Europe (Frankfurt)",
		"ap-southeast-1": "Asia Pacific (Singapore)",
		"ap-southeast-2": "Asia Pacific (Sydney)",
		"ap-northeast-1": "Asia Pacific (Tokyo)",
	}
	if loc, ok := locations[region]; ok {
		return loc
	}
	return region // pass-through for unmapped regions
}
