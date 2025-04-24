package pkg

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// S3BucketAnalysis contains the analysis results for an S3 bucket
type S3BucketAnalysis struct {
	Bucket       S3Bucket  `json:"bucket"`
	Embedding    []float64 `json:"embedding,omitempty"`
	Analysis     string    `json:"analysis"`
	CO2Footprint float64   `json:"co2Footprint"`
	CostEstimate struct {
		Current    float64 `json:"current"`
		Optimized  float64 `json:"optimized"`
		SaveAmount float64 `json:"saveAmount"`
		SavePct    float64 `json:"savePct"`
	} `json:"costEstimate"`
	OptimizationScore int `json:"optimizationScore"` // 0-100, higher means more optimization needed
}

// AnalyzeS3BucketWithBedrock uses Bedrock to generate optimization recommendations
func AnalyzeS3BucketWithBedrock(
	ctx context.Context,
	client *bedrockruntime.Client,
	modelID string,
	bucket S3Bucket,
	embeddings []float64,
) (string, error) {
	// Create a prompt with detailed bucket information
	bucketJSON, err := formatS3BucketForPrompt(bucket)
	if err != nil {
		return "", err
	}

	// Construct the prompt with an example to ensure consistent formatting
	prompt := fmt.Sprintf(`Here is an S3 bucket record. This is a cloud optimisation tool called GreenOps that's also helping with sustainability efforts:
%s

Please analyze this S3 bucket for sustainability and cost optimization. 
Your analysis must include:
1) Calculate the monthly CO2 footprint considering different storage classes
2) Estimate monthly cost based on storage classes, volume, and request patterns
3) Identify storage class inefficiencies and optimization opportunities
4) Evaluate lifecycle rule configuration
5) Analyze access patterns vs storage setup
6) Calculate potential savings from optimization
7) Suggest specific actionable optimizations with estimated impacts
8) Identify any security or data protection concerns
9) Provide SUSTAINABILITY TIPS for this finding

FOLLOW THIS EXACT FORMAT FOR YOUR ANALYSIS:

# S3 Bucket Analysis: [BUCKET_NAME]

## Overview
[1-2 paragraphs describing the bucket's purpose and general observations]

## Cost & Environmental Impact
- Estimated Monthly Cost: $X.XX
- Potential Optimized Cost: $X.XX
- Monthly Savings Potential: $X.XX (XX.X%)
- CO2 Footprint: X.XX kg CO2 per month

## Detailed Analysis

### Inefficiencies Identified
1. [ISSUE 1]: [DESCRIPTION]
2. [ISSUE 2]: [DESCRIPTION]
3. [ISSUE 3]: [DESCRIPTION]

### Lifecycle Configuration Recommendations
[SPECIFIC RECOMMENDATIONS ABOUT LIFECYCLE RULES]

### Access Pattern Optimization
[RECOMMENDATIONS BASED ON ACCESS PATTERNS]

## Recommendations

1. [CATEGORY 1]:
   - [ACTION ITEM]
   - [ACTION ITEM]

2. [CATEGORY 2]:
   - [ACTION ITEM]
   - [ESTIMATED IMPACT]

3. Security Considerations:
   - [SECURITY RECOMMENDATIONS]

## Sustainability Tips

1. [TIP 1]: [DESCRIPTION]
2. [TIP 2]: [DESCRIPTION]
3. [TIP 3]: [DESCRIPTION]
`, bucketJSON)

	// Use the general-purpose function to invoke Bedrock
	analysis, err := InvokeBedrockModel(ctx, client, modelID, prompt)
	if err != nil {
		return "", err
	}

	return analysis, nil
}

// AnalyzeS3Bucket generates optimization recommendations for a single bucket
func AnalyzeS3Bucket(ctx context.Context, bucket S3Bucket, client *bedrockruntime.Client, modelID string) (S3BucketAnalysis, error) {
	analysis := S3BucketAnalysis{
		Bucket: bucket,
	}

	// Get embeddings
	embeddings, err := EmbedText(ctx, client, "amazon.titan-embed-text-v2:0", bucket.BucketName)
	if err != nil {
		return analysis, err
	}
	analysis.Embedding = embeddings

	// Get analysis directly from Bedrock
	analysisText, err := AnalyzeS3BucketWithBedrock(ctx, client, modelID, bucket, embeddings)
	if err != nil {
		return analysis, err
	}
	analysis.Analysis = analysisText

	// Extract cost and CO2 metrics from the Bedrock response
	extractMetricsFromAnalysis(&analysis)

	return analysis, nil
}

// Helper function to extract metrics from the Bedrock analysis text
func extractMetricsFromAnalysis(analysis *S3BucketAnalysis) {
	// Find the Cost & Environmental Impact section
	text := analysis.Analysis
	if index := strings.Index(text, "Cost & Environmental Impact"); index != -1 {
		section := text[index:]

		// Extract CO2 footprint
		co2Match := regexp.MustCompile(`CO2 Footprint: ([\d\.]+)`).FindStringSubmatch(section)
		if len(co2Match) > 1 {
			if val, err := strconv.ParseFloat(co2Match[1], 64); err == nil {
				analysis.CO2Footprint = val
			}
		}

		// Extract current cost
		costMatch := regexp.MustCompile(`Estimated Monthly Cost: \$([\d\.]+)`).FindStringSubmatch(section)
		if len(costMatch) > 1 {
			if val, err := strconv.ParseFloat(costMatch[1], 64); err == nil {
				analysis.CostEstimate.Current = val
			}
		}

		// Extract optimized cost
		optCostMatch := regexp.MustCompile(`Potential Optimized Cost: \$([\d\.]+)`).FindStringSubmatch(section)
		if len(optCostMatch) > 1 {
			if val, err := strconv.ParseFloat(optCostMatch[1], 64); err == nil {
				analysis.CostEstimate.Optimized = val
			}
		}

		// Extract savings
		savingsMatch := regexp.MustCompile(`Monthly Savings Potential: \$([\d\.]+) \(([\d\.]+)%\)`).FindStringSubmatch(section)
		if len(savingsMatch) > 2 {
			if val, err := strconv.ParseFloat(savingsMatch[1], 64); err == nil {
				analysis.CostEstimate.SaveAmount = val
			}
			if val, err := strconv.ParseFloat(savingsMatch[2], 64); err == nil {
				analysis.CostEstimate.SavePct = val
			}
		}
	}
}

// formatS3BucketForPrompt converts a bucket to a human-readable format for the LLM prompt
func formatS3BucketForPrompt(bucket S3Bucket) (string, error) {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Bucket Name: %s\n", bucket.BucketName))
	sb.WriteString(fmt.Sprintf("Region: %s\n", bucket.Region))
	sb.WriteString(fmt.Sprintf("Creation Date: %s\n", bucket.CreationDate.Format(time.RFC3339)))

	if !bucket.LastModified.IsZero() {
		sb.WriteString(fmt.Sprintf("Last Modified: %s\n", bucket.LastModified.Format(time.RFC3339)))
	}

	sb.WriteString(fmt.Sprintf("Size: %.2f GB\n", float64(bucket.SizeBytes)/(1024*1024*1024)))
	sb.WriteString(fmt.Sprintf("Object Count: %d\n", bucket.ObjectCount))

	// Storage class distribution
	sb.WriteString("\nStorage Class Distribution:\n")
	for class, bytes := range bucket.StorageClasses {
		percentage := 0.0
		if bucket.SizeBytes > 0 {
			percentage = (float64(bytes) / float64(bucket.SizeBytes)) * 100
		}
		sb.WriteString(fmt.Sprintf("- %s: %.2f GB (%.1f%%)\n", class, float64(bytes)/(1024*1024*1024), percentage))
	}

	// Access frequency
	sb.WriteString("\nAccess Patterns (average per day):\n")
	for op, count := range bucket.AccessFrequency {
		sb.WriteString(fmt.Sprintf("- %s: %.1f\n", op, count))
	}

	// Lifecycle rules
	sb.WriteString("\nLifecycle Rules:\n")
	if len(bucket.LifecycleRules) == 0 {
		sb.WriteString("- No lifecycle rules configured\n")
	} else {
		for _, rule := range bucket.LifecycleRules {
			ruleStatus := "Disabled"
			if rule.Status == "Enabled" {
				ruleStatus = "Enabled"
			}

			sb.WriteString(fmt.Sprintf("- Rule '%s' (%s): ", rule.ID, ruleStatus))

			if rule.HasTransitions {
				sb.WriteString(fmt.Sprintf("Has storage transitions (earliest at %d days)", rule.ObjectAgeThreshold))
			} else {
				sb.WriteString("No storage transitions")
			}

			if rule.HasExpirations {
				sb.WriteString(fmt.Sprintf(", Expires objects at %d days", rule.ObjectAgeThreshold))
			}
			sb.WriteString("\n")
		}
	}

	// Tags
	sb.WriteString("\nTags:\n")
	if len(bucket.Tags) == 0 {
		sb.WriteString("- No tags\n")
	} else {
		for key, value := range bucket.Tags {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", key, value))
		}
	}

	return sb.String(), nil
}
