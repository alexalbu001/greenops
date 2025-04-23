package pkg

import (
	"context"
	"fmt"
	"math"
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

// AnalyzeS3Bucket generates optimization recommendations for a single bucket
func AnalyzeS3Bucket(ctx context.Context, bucket S3Bucket) (S3BucketAnalysis, error) {
	analysis := S3BucketAnalysis{
		Bucket: bucket,
	}

	// Calculate CO2 footprint
	co2Footprint := calculateS3CO2Footprint(bucket)
	analysis.CO2Footprint = co2Footprint

	// Estimate current and optimized costs
	currentCost, optimizedCost := estimateS3Costs(bucket)
	analysis.CostEstimate.Current = currentCost
	analysis.CostEstimate.Optimized = optimizedCost
	analysis.CostEstimate.SaveAmount = currentCost - optimizedCost

	// Calculate percentage savings (avoid division by zero)
	if currentCost > 0 {
		analysis.CostEstimate.SavePct = (analysis.CostEstimate.SaveAmount / currentCost) * 100
	}

	// Calculate optimization score (0-100, higher means more opportunity to optimize)
	analysis.OptimizationScore = calculateOptimizationScore(bucket, analysis.CostEstimate.SavePct)

	// Generate human-readable analysis text
	analysisText, err := generateS3Analysis(bucket, analysis)
	if err != nil {
		return analysis, err
	}
	analysis.Analysis = analysisText

	return analysis, nil
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

	// Calculate cost and CO2 estimates
	co2Footprint := calculateS3CO2Footprint(bucket)
	currentCost, optimizedCost := estimateS3Costs(bucket)
	savingsPercent := 0.0
	if currentCost > 0 {
		savingsPercent = ((currentCost - optimizedCost) / currentCost) * 100
	}

	// Construct the prompt
	prompt := fmt.Sprintf(`Here is an S3 bucket record.Keep a clean formatting and dont use any "*" or "#". This is a cloud optimisation tool thats also helping with sustenability efforts :
%s

Metrics:
- CO2 Footprint: %.2f kg CO2 per month
- Current Cost: $%.2f per month
- Potential Optimized Cost: $%.2f per month
- Potential Savings: %.1f%%

1) Identify storage class inefficiencies and optimization opportunities.
2) Evaluate lifecycle rule configuration.
3) Analyze access patterns vs storage setup.
4) Suggest specific actionable optimizations with estimated impacts.
5) Identify any security or data protection concerns.
6) Provide SUSTENABILITY TIPS for this finding
`, bucketJSON, co2Footprint, currentCost, optimizedCost, savingsPercent)

	// Use the Bedrock model to generate analysis (similar to AnalyzeInstance function)
	analysis, err := AnalyzeInstance(ctx, client, modelID, prompt, 0)
	if err != nil {
		return "", err
	}

	return analysis, nil
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

// generateS3Analysis creates a detailed analysis of the bucket without using Bedrock
func generateS3Analysis(bucket S3Bucket, analysis S3BucketAnalysis) (string, error) {
	var sb strings.Builder

	// Header
	sb.WriteString(fmt.Sprintf("# S3 Bucket Analysis: %s\n\n", bucket.BucketName))

	// Bucket details
	sb.WriteString(" Bucket Details\n")
	sb.WriteString(fmt.Sprintf("- Name: %s\n", bucket.BucketName))
	sb.WriteString(fmt.Sprintf("- Region: %s\n", bucket.Region))
	sb.WriteString(fmt.Sprintf("- Created: %s\n", bucket.CreationDate.Format("January 2, 2006")))
	sb.WriteString(fmt.Sprintf("- Size: %.2f GB\n", float64(bucket.SizeBytes)/(1024*1024*1024)))
	sb.WriteString(fmt.Sprintf("- Objects: %d\n", bucket.ObjectCount))

	// Last activity
	if !bucket.LastModified.IsZero() {
		daysSinceModified := int(time.Since(bucket.LastModified).Hours() / 24)
		sb.WriteString(fmt.Sprintf("- Last Modified: %s (%d days ago)\n",
			bucket.LastModified.Format("January 2, 2006"), daysSinceModified))
	}

	// Tags if they exist
	if len(bucket.Tags) > 0 {
		sb.WriteString("- Tags: ")
		tagCount := 0
		for k, v := range bucket.Tags {
			if tagCount > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%s=%s", k, v))
			tagCount++
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n")

	// Storage class analysis
	sb.WriteString(" 1. Storage Class Distribution\n\n")
	hasStandardStorage := false
	standardPct := 0.0

	if bucket.SizeBytes > 0 {
		if standardBytes, ok := bucket.StorageClasses["STANDARD"]; ok && standardBytes > 0 {
			standardPct = (float64(standardBytes) / float64(bucket.SizeBytes)) * 100
			hasStandardStorage = true
		}
	}

	for class, bytes := range bucket.StorageClasses {
		percentage := 0.0
		if bucket.SizeBytes > 0 {
			percentage = (float64(bytes) / float64(bucket.SizeBytes)) * 100
		}
		sb.WriteString(fmt.Sprintf("- %s: %.2f GB (%.1f%%)\n", class, float64(bytes)/(1024*1024*1024), percentage))
	}

	sb.WriteString("\n")

	// Analysis based on storage classes
	if hasStandardStorage && standardPct > 75 && bucket.SizeBytes > 5*1024*1024*1024 { // > 5GB
		sb.WriteString("Finding: This bucket primarily uses STANDARD storage class, which is the most expensive.\n\n")
	}

	// Lifecycle rule analysis
	sb.WriteString(" 2. Lifecycle Configuration\n\n")

	if len(bucket.LifecycleRules) == 0 {
		sb.WriteString("Finding: No lifecycle rules are configured for this bucket. ")

		if bucket.SizeBytes > 1*1024*1024*1024 && hasStandardStorage { // > 1GB
			sb.WriteString("Adding lifecycle rules could reduce costs by transitioning objects to cheaper storage classes.\n\n")
		} else {
			sb.WriteString("Consider adding lifecycle rules if you expect the bucket to grow.\n\n")
		}
	} else {
		sb.WriteString(fmt.Sprintf("This bucket has %d lifecycle rule(s):\n\n", len(bucket.LifecycleRules)))

		for _, rule := range bucket.LifecycleRules {
			ruleStatus := "Disabled"
			if rule.Status == "Enabled" {
				ruleStatus = "Enabled"
			}

			sb.WriteString(fmt.Sprintf("- Rule '%s' (%s): ", rule.ID, ruleStatus))

			if rule.HasTransitions {
				sb.WriteString(fmt.Sprintf("Transitions begin at %d days", rule.ObjectAgeThreshold))
			} else {
				sb.WriteString("No storage transitions")
			}

			if rule.HasExpirations {
				sb.WriteString(fmt.Sprintf(", Objects expire at %d days", rule.ObjectAgeThreshold))
			}
			sb.WriteString("\n")
		}

		sb.WriteString("\n")

		// Analyze if the lifecycle rules are effective
		hasEnabledTransitions := false
		for _, rule := range bucket.LifecycleRules {
			if rule.Status == "Enabled" && rule.HasTransitions {
				hasEnabledTransitions = true
				break
			}
		}

		if !hasEnabledTransitions && hasStandardStorage && bucket.SizeBytes > 1*1024*1024*1024 {
			sb.WriteString("Finding: No enabled lifecycle rules with storage transitions were found. ")
			sb.WriteString("Consider enabling transitions to optimize storage costs.\n\n")
		}
	}

	// Access pattern analysis
	sb.WriteString(" 3. Access Patterns\n\n")

	// Get access values or set defaults
	getRequests := bucket.AccessFrequency["GetRequests"]
	putRequests := bucket.AccessFrequency["PutRequests"]
	deleteRequests := bucket.AccessFrequency["DeleteRequests"]

	sb.WriteString(fmt.Sprintf("- GET Operations: %.1f per day\n", getRequests))
	sb.WriteString(fmt.Sprintf("- PUT Operations: %.1f per day\n", putRequests))
	sb.WriteString(fmt.Sprintf("- DELETE Operations: %.1f per day\n", deleteRequests))
	sb.WriteString("\n")

	// Analyze access patterns
	if getRequests < 1.0 && putRequests < 1.0 && hasStandardStorage && standardPct > 50 {
		sb.WriteString("Finding: This bucket has very low access frequency but primarily uses STANDARD storage. ")
		sb.WriteString("Consider transitioning to STANDARD_IA or GLACIER storage classes.\n\n")
	} else if getRequests > 1000 && putRequests < 10 {
		sb.WriteString("Finding: This bucket has high read but low write activity, ")
		sb.WriteString("making it a good candidate for INTELLIGENT_TIERING or read-optimized configurations.\n\n")
	}

	// Cost and CO2 analysis
	sb.WriteString(" 4. Cost & Environmental Impact\n\n")
	sb.WriteString(fmt.Sprintf("- Estimated Monthly Cost: $%.2f\n", analysis.CostEstimate.Current))
	sb.WriteString(fmt.Sprintf("- Potential Optimized Cost: $%.2f\n", analysis.CostEstimate.Optimized))
	sb.WriteString(fmt.Sprintf("- Monthly Savings Potential: $%.2f (%.1f%%)\n",
		analysis.CostEstimate.SaveAmount, analysis.CostEstimate.SavePct))
	sb.WriteString(fmt.Sprintf("- CO2 Footprint: %.2f kg CO2 per month\n", analysis.CO2Footprint))
	sb.WriteString("\n")

	// Recommendations
	sb.WriteString(" 5. Recommendations\n\n")

	if analysis.CostEstimate.SavePct > 30 {
		sb.WriteString("# High Priority\n\n")
	} else if analysis.CostEstimate.SavePct > 10 {
		sb.WriteString("# Medium Priority\n\n")
	} else {
		sb.WriteString("# Recommendations\n\n")
	}

	// Generate recommendations based on findings
	recommendationCount := 0

	// Lifecycle rules recommendation
	if len(bucket.LifecycleRules) == 0 && bucket.SizeBytes > 1*1024*1024*1024 {
		recommendationCount++
		sb.WriteString(fmt.Sprintf("%d. Add lifecycle rules to transition objects to cheaper storage classes after 30-90 days,", recommendationCount))
		sb.WriteString(fmt.Sprintf(" potentially saving $%.2f per month.\n\n", analysis.CostEstimate.SaveAmount*0.7))
	}

	// Standard storage recommendation for low-access buckets
	if hasStandardStorage && standardPct > 70 && getRequests < 1.0 && bucket.SizeBytes > 1*1024*1024*1024 {
		recommendationCount++
		sb.WriteString(fmt.Sprintf("%d. Move rarely accessed data to STANDARD_IA or GLACIER storage classes.", recommendationCount))
		sb.WriteString(fmt.Sprintf(" This could save approximately $%.2f per month.\n\n", analysis.CostEstimate.SaveAmount*0.8))
	}

	// Intelligent tiering recommendation
	if hasStandardStorage && standardPct > 60 && bucket.SizeBytes > 10*1024*1024*1024 {
		recommendationCount++
		sb.WriteString(fmt.Sprintf("%d. Enable INTELLIGENT_TIERING for this bucket to automatically optimize", recommendationCount))
		sb.WriteString(" storage costs based on access patterns.\n\n")
	}

	// Tag recommendation
	if len(bucket.Tags) == 0 {
		recommendationCount++
		sb.WriteString(fmt.Sprintf("%d. Add tags to this bucket for better cost allocation and management ", recommendationCount))
		sb.WriteString("(e.g., Environment=Prod, Department=X, Project=Y).\n\n")
	}

	// Default recommendation if none of the above
	if recommendationCount == 0 {
		sb.WriteString("1. Monitor usage patterns and revisit optimization opportunities as the bucket grows or usage patterns change.\n\n")
	}

	return sb.String(), nil
}

// calculateS3CO2Footprint estimates the carbon impact of an S3 bucket
// This is a simplified model based on storage volume and region
func calculateS3CO2Footprint(bucket S3Bucket) float64 {
	// Base CO2 calculation factors
	// These are approximations - actual CO2 impact depends on many factors
	const (
		// kg CO2 per GB-month for different storage classes
		standardCO2PerGB        = 0.0024
		standardIACO2PerGB      = 0.0012
		glacierCO2PerGB         = 0.0008
		deepArchiveCO2PerGB     = 0.0004
		intelligentTierCO2PerGB = 0.0018

		// Regional multipliers (simplified)
		// Some regions use more renewable energy than others
		defaultRegionMultiplier = 1.0
	)

	// Get regional multiplier (simplified - real implementation would use actual region-specific data)
	regionMultiplier := defaultRegionMultiplier
	// In a real implementation, you'd have a map of regions to CO2 multipliers
	// based on the energy mix of each AWS region

	// Calculate total CO2 for the bucket
	totalCO2 := 0.0
	totalGB := float64(bucket.SizeBytes) / (1024 * 1024 * 1024)

	// If storage class breakdown is available, use it
	if len(bucket.StorageClasses) > 0 {
		for class, bytes := range bucket.StorageClasses {
			gbInClass := float64(bytes) / (1024 * 1024 * 1024)
			co2PerGB := standardCO2PerGB // Default to standard

			switch class {
			case "STANDARD":
				co2PerGB = standardCO2PerGB
			case "STANDARD_IA", "ONEZONE_IA":
				co2PerGB = standardIACO2PerGB
			case "GLACIER", "GLACIER_IR":
				co2PerGB = glacierCO2PerGB
			case "DEEP_ARCHIVE":
				co2PerGB = deepArchiveCO2PerGB
			case "INTELLIGENT_TIERING":
				co2PerGB = intelligentTierCO2PerGB
			}

			totalCO2 += gbInClass * co2PerGB * regionMultiplier
		}
	} else {
		// If no breakdown, assume all storage is standard
		totalCO2 = totalGB * standardCO2PerGB * regionMultiplier
	}

	// Add CO2 from request operations (simplified)
	getRequestsCO2 := bucket.AccessFrequency["GetRequests"] * 30 * 0.0000001 // Very small CO2 per GET
	putRequestsCO2 := bucket.AccessFrequency["PutRequests"] * 30 * 0.0000002 // Slightly more for PUT

	totalCO2 += getRequestsCO2 + putRequestsCO2

	return totalCO2
}

// estimateS3Costs calculates current and potential optimized costs
func estimateS3Costs(bucket S3Bucket) (currentCost, optimizedCost float64) {
	// Simplified pricing model - actual AWS pricing varies by region
	const (
		// USD per GB-month for different storage classes
		standardPricePerGB        = 0.023
		standardIAPricePerGB      = 0.0125
		glacierPricePerGB         = 0.004
		deepArchivePricePerGB     = 0.00099
		intelligentTierPricePerGB = 0.023 // Base tier, auto-optimizes over time

		// Request pricing (simplified)
		getPricePerK          = 0.0004 // per 1000 requests
		putPricePerK          = 0.005  // per 1000 requests
		glacierRetrievalPerGB = 0.03   // For Glacier retrieval

		// Data transfer (simplified)
		outboundPricePerGB = 0.09 // Outbound data transfer
	)

	// Calculate storage costs
	currentStorageCost := 0.0

	if len(bucket.StorageClasses) > 0 {
		for class, bytes := range bucket.StorageClasses {
			gbInClass := float64(bytes) / (1024 * 1024 * 1024)
			pricePerGB := standardPricePerGB // Default to standard

			switch class {
			case "STANDARD":
				pricePerGB = standardPricePerGB
			case "STANDARD_IA", "ONEZONE_IA":
				pricePerGB = standardIAPricePerGB
			case "GLACIER", "GLACIER_IR":
				pricePerGB = glacierPricePerGB
			case "DEEP_ARCHIVE":
				pricePerGB = deepArchivePricePerGB
			case "INTELLIGENT_TIERING":
				pricePerGB = intelligentTierPricePerGB
			}

			currentStorageCost += gbInClass * pricePerGB
		}
	} else {
		// If no breakdown, assume all storage is standard
		totalGB := float64(bucket.SizeBytes) / (1024 * 1024 * 1024)
		currentStorageCost = totalGB * standardPricePerGB
	}

	// Calculate request costs (monthly)
	getRequests := bucket.AccessFrequency["GetRequests"] * 30 // Monthly
	putRequests := bucket.AccessFrequency["PutRequests"] * 30 // Monthly

	getRequestsCost := (getRequests / 1000) * getPricePerK
	putRequestsCost := (putRequests / 1000) * putPricePerK

	// Calculate current total monthly cost
	currentCost = currentStorageCost + getRequestsCost + putRequestsCost

	// Calculate optimized cost

	// Determine optimal storage class mix based on access patterns
	optimizedStorageCost := 0.0
	totalGB := float64(bucket.SizeBytes) / (1024 * 1024 * 1024)

	// Very low access frequency - move most to Glacier
	if getRequests < 10 && putRequests < 10 {
		// Keep 5% in Standard for occasional access
		standardGB := totalGB * 0.05
		glacierGB := totalGB * 0.95

		optimizedStorageCost = (standardGB * standardPricePerGB) + (glacierGB * glacierPricePerGB)

		// Add in potential retrieval costs
		optimizedStorageCost += (glacierGB * 0.01 * glacierRetrievalPerGB) // Assume 1% retrieved monthly
	} else if getRequests < 100 && putRequests < 50 {
		// Low access frequency - use primarily Standard IA
		standardGB := totalGB * 0.2
		standardIAGB := totalGB * 0.8

		optimizedStorageCost = (standardGB * standardPricePerGB) + (standardIAGB * standardIAPricePerGB)
	} else if getRequests < 1000 {
		// Medium access with a mix, or intelligent tiering would be best
		// Intelligent tiering would be ideal, but simulating a mix for simplicity
		standardGB := totalGB * 0.4
		standardIAGB := totalGB * 0.6

		optimizedStorageCost = (standardGB * standardPricePerGB) + (standardIAGB * standardIAPricePerGB)
	} else {
		// High access - keep mostly in standard
		// Some opportunity for optimization but less dramatic
		standardGB := totalGB * 0.85
		standardIAGB := totalGB * 0.15

		optimizedStorageCost = (standardGB * standardPricePerGB) + (standardIAGB * standardIAPricePerGB)
	}

	// Same request costs in optimized scenario
	optimizedCost = optimizedStorageCost + getRequestsCost + putRequestsCost

	// If somehow optimized ended up more expensive, default to current cost
	if optimizedCost > currentCost {
		optimizedCost = currentCost * 0.95 // Assume at least 5% saving is possible
	}

	return currentCost, optimizedCost
}

// calculateOptimizationScore produces a 0-100 score for optimization potential
func calculateOptimizationScore(bucket S3Bucket, savingsPct float64) int {
	score := 0

	// Base score from potential savings percentage
	// 0% savings = 0 points, 50% or higher savings = 50 points
	score += int(math.Min(savingsPct, 50))

	// No lifecycle rules = +20 points (if bucket is of significant size)
	if len(bucket.LifecycleRules) == 0 && bucket.SizeBytes > 1*1024*1024*1024 {
		score += 20
	}

	// Primarily standard storage = +15 points
	standardPct := 0.0
	if bucket.SizeBytes > 0 {
		if standardBytes, ok := bucket.StorageClasses["STANDARD"]; ok {
			standardPct = (float64(standardBytes) / float64(bucket.SizeBytes)) * 100
		}
	}

	if standardPct > 75 {
		score += 15
	}

	// Low access frequency with standard storage = +15 points
	getRequests := bucket.AccessFrequency["GetRequests"]
	putRequests := bucket.AccessFrequency["PutRequests"]

	if getRequests < 1.0 && putRequests < 1.0 && standardPct > 50 {
		score += 15
	}

	// Cap at 100
	if score > 100 {
		score = 100
	}

	return score
}
