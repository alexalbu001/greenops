package pkg

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// RDSInstanceAnalysis contains the analysis results for an RDS instance
type RDSInstanceAnalysis struct {
	Instance     RDSInstance `json:"instance"`
	Embedding    []float64   `json:"embedding,omitempty"`
	Analysis     string      `json:"analysis"`
	CO2Footprint float64     `json:"co2Footprint"`
	CostEstimate struct {
		Current    float64 `json:"current"`
		Optimized  float64 `json:"optimized"`
		SaveAmount float64 `json:"saveAmount"`
		SavePct    float64 `json:"savePct"`
	} `json:"costEstimate"`
	OptimizationScore int `json:"optimizationScore"` // 0-100, higher means more optimization needed
}

// AnalyzeRDSInstance generates optimization recommendations for a single RDS instance
func AnalyzeRDSInstance(ctx context.Context, instance RDSInstance) (RDSInstanceAnalysis, error) {
	analysis := RDSInstanceAnalysis{
		Instance: instance,
	}

	// Calculate CO2 footprint
	co2Footprint := calculateRDSCO2Footprint(instance)
	analysis.CO2Footprint = co2Footprint

	// Estimate current and optimized costs
	currentCost, optimizedCost := estimateRDSCosts(instance)
	analysis.CostEstimate.Current = currentCost
	analysis.CostEstimate.Optimized = optimizedCost
	analysis.CostEstimate.SaveAmount = currentCost - optimizedCost

	// Calculate percentage savings (avoid division by zero)
	if currentCost > 0 {
		analysis.CostEstimate.SavePct = (analysis.CostEstimate.SaveAmount / currentCost) * 100
	}

	// Calculate optimization score (0-100, higher means more opportunity to optimize)
	analysis.OptimizationScore = calculateRDSOptimizationScore(instance, analysis.CostEstimate.SavePct)

	// Generate human-readable analysis text
	analysisText, err := generateRDSAnalysis(instance, analysis)
	if err != nil {
		return analysis, err
	}
	analysis.Analysis = analysisText

	return analysis, nil
}

// AnalyzeRDSInstanceWithBedrock uses Bedrock to generate optimization recommendations
func AnalyzeRDSInstanceWithBedrock(
	ctx context.Context,
	client *bedrockruntime.Client,
	modelID string,
	instance RDSInstance,
	embeddings []float64,
) (string, error) {
	// Create a prompt with detailed instance information
	instanceJSON, err := formatRDSInstanceForPrompt(instance)
	if err != nil {
		return "", err
	}

	// Calculate cost and CO2 estimates
	co2Footprint := calculateRDSCO2Footprint(instance)
	currentCost, optimizedCost := estimateRDSCosts(instance)
	savingsPercent := 0.0
	if currentCost > 0 {
		savingsPercent = ((currentCost - optimizedCost) / currentCost) * 100
	}

	// Construct the prompt
	prompt := fmt.Sprintf(`Here is an RDS instance record:
%s

Metrics:
- CO2 Footprint: %.2f kg CO2 per month
- Current Cost: $%.2f per month
- Potential Optimized Cost: $%.2f per month
- Potential Savings: %.1f%%

1) Identify inefficiencies (over-provisioning, low utilization, etc.)
2) Estimate monthly CO2 footprint based on instance size and usage.
3) Suggest specific actions for rightsizing or optimization.
4) Identify any performance or availability concerns.
`, instanceJSON, co2Footprint, currentCost, optimizedCost, savingsPercent)

	// Use the Bedrock model to generate analysis (similar to AnalyzeInstance function)
	analysis, err := AnalyzeInstance(ctx, client, modelID, prompt, 0)
	if err != nil {
		return "", err
	}

	return analysis, nil
}

// formatRDSInstanceForPrompt converts an RDS instance to a human-readable format for the LLM prompt
func formatRDSInstanceForPrompt(instance RDSInstance) (string, error) {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Instance ID: %s\n", instance.InstanceID))
	sb.WriteString(fmt.Sprintf("Instance Type: %s\n", instance.InstanceType))
	sb.WriteString(fmt.Sprintf("Engine: %s %s\n", instance.Engine, instance.EngineVersion))
	sb.WriteString(fmt.Sprintf("Storage Type: %s\n", instance.StorageType))
	sb.WriteString(fmt.Sprintf("Allocated Storage: %d GB\n", instance.AllocatedStorage))
	sb.WriteString(fmt.Sprintf("Multi-AZ: %t\n", instance.MultiAZ))
	sb.WriteString(fmt.Sprintf("Status: %s\n", instance.Status))
	sb.WriteString(fmt.Sprintf("Region: %s\n", instance.Region))

	if !instance.LaunchTime.IsZero() {
		sb.WriteString(fmt.Sprintf("Launch Time: %s\n", instance.LaunchTime.Format(time.RFC3339)))
		// Calculate age
		age := time.Since(instance.LaunchTime)
		sb.WriteString(fmt.Sprintf("Age: %.1f days\n", age.Hours()/24))
	}

	// Metrics
	sb.WriteString(fmt.Sprintf("CPU Utilization (7-day avg): %.1f%%\n", instance.CPUAvg7d))
	sb.WriteString(fmt.Sprintf("Database Connections (7-day avg): %.1f\n", instance.ConnectionsAvg7d))
	sb.WriteString(fmt.Sprintf("IOPS (7-day avg): %.1f\n", instance.IOPSAvg7d))
	sb.WriteString(fmt.Sprintf("Storage Used: %.1f%%\n", instance.StorageUsed))

	// Tags
	if len(instance.Tags) > 0 {
		sb.WriteString("\nTags:\n")
		for k, v := range instance.Tags {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", k, v))
		}
	}

	return sb.String(), nil
}

// generateRDSAnalysis creates a detailed analysis of the RDS instance without using Bedrock
func generateRDSAnalysis(instance RDSInstance, analysis RDSInstanceAnalysis) (string, error) {
	var sb strings.Builder

	// Header
	sb.WriteString(fmt.Sprintf("# RDS Instance Analysis: %s\n\n", instance.InstanceID))

	// Instance details
	sb.WriteString("## Instance Details\n")
	sb.WriteString(fmt.Sprintf("- **ID**: %s\n", instance.InstanceID))
	sb.WriteString(fmt.Sprintf("- **Type**: %s\n", instance.InstanceType))
	sb.WriteString(fmt.Sprintf("- **Engine**: %s %s\n", instance.Engine, instance.EngineVersion))
	sb.WriteString(fmt.Sprintf("- **Storage**: %d GB (%s)\n", instance.AllocatedStorage, instance.StorageType))
	sb.WriteString(fmt.Sprintf("- **Multi-AZ**: %t\n", instance.MultiAZ))

	if !instance.LaunchTime.IsZero() {
		sb.WriteString(fmt.Sprintf("- **Launch Time**: %s\n", instance.LaunchTime.Format("January 2, 2006")))
		age := time.Since(instance.LaunchTime)
		sb.WriteString(fmt.Sprintf("- **Age**: %.1f days\n", age.Hours()/24))
	}

	sb.WriteString("\n")

	// Performance metrics
	sb.WriteString("## 1. Performance Analysis\n\n")

	// CPU utilization
	cpuStatus := "Normal"
	if instance.CPUAvg7d < 5 {
		cpuStatus = "Severely underutilized"
	} else if instance.CPUAvg7d < 20 {
		cpuStatus = "Underutilized"
	} else if instance.CPUAvg7d > 75 {
		cpuStatus = "High utilization"
	}

	sb.WriteString(fmt.Sprintf("- **CPU Utilization**: %.1f%% (%s)\n", instance.CPUAvg7d, cpuStatus))
	sb.WriteString(fmt.Sprintf("- **Database Connections**: %.1f average\n", instance.ConnectionsAvg7d))
	sb.WriteString(fmt.Sprintf("- **IOPS**: %.1f operations per second\n", instance.IOPSAvg7d))
	sb.WriteString(fmt.Sprintf("- **Storage Used**: %.1f%%\n\n", instance.StorageUsed))

	// Inefficiency analysis
	sb.WriteString("## 2. Inefficiency Analysis\n\n")

	if instance.CPUAvg7d < 20 {
		sb.WriteString(fmt.Sprintf("**Finding**: This RDS instance shows **low CPU utilization** at only %.1f%%. ", instance.CPUAvg7d))
		sb.WriteString("This suggests the instance may be oversized for its current workload.\n\n")
	}

	if instance.StorageUsed < 50 && instance.AllocatedStorage > 100 {
		sb.WriteString(fmt.Sprintf("**Finding**: Storage is **overprovisioned** with only %.1f%% of allocated storage in use. ", instance.StorageUsed))
		sb.WriteString("This indicates potential for optimization.\n\n")
	}

	if !instance.MultiAZ && isProductionDatabase(instance) {
		sb.WriteString("**Finding**: This appears to be a production database but is not using Multi-AZ deployment, which could impact availability.\n\n")
	}

	// Cost and environmental impact
	sb.WriteString("## 3. Cost & Environmental Impact\n\n")
	sb.WriteString(fmt.Sprintf("- **Estimated Monthly Cost**: $%.2f\n", analysis.CostEstimate.Current))
	sb.WriteString(fmt.Sprintf("- **Potential Optimized Cost**: $%.2f\n", analysis.CostEstimate.Optimized))
	sb.WriteString(fmt.Sprintf("- **Monthly Savings Potential**: $%.2f (%.1f%%)\n",
		analysis.CostEstimate.SaveAmount, analysis.CostEstimate.SavePct))
	sb.WriteString(fmt.Sprintf("- **CO2 Footprint**: %.2f kg CO2 per month\n\n", analysis.CO2Footprint))

	// Recommendations
	sb.WriteString("## 4. Recommendations\n\n")

	// Generate recommendations based on findings
	if analysis.CostEstimate.SavePct > 30 {
		sb.WriteString("### High Priority\n\n")
	} else if analysis.CostEstimate.SavePct > 10 {
		sb.WriteString("### Medium Priority\n\n")
	} else {
		sb.WriteString("### Recommendations\n\n")
	}

	recommendationCount := 0

	// Right-sizing recommendation based on CPU
	if instance.CPUAvg7d < 5 {
		recommendationCount++
		sb.WriteString(fmt.Sprintf("%d. **Downsize instance class** - Consider moving to a smaller instance type. ", recommendationCount))
		sb.WriteString(fmt.Sprintf("With only %.1f%% CPU utilization, a smaller instance would likely meet your requirements.\n\n", instance.CPUAvg7d))
	} else if instance.CPUAvg7d < 20 {
		recommendationCount++
		sb.WriteString(fmt.Sprintf("%d. **Evaluate instance size** - With %.1f%% average CPU utilization, ", recommendationCount, instance.CPUAvg7d))
		sb.WriteString("you may be able to reduce instance size for cost savings.\n\n")
	}

	// Storage recommendations
	if instance.StorageUsed < 30 && instance.AllocatedStorage > 100 {
		recommendationCount++
		sb.WriteString(fmt.Sprintf("%d. **Reduce allocated storage** - Current usage is only %.1f%% ", recommendationCount, instance.StorageUsed))
		sb.WriteString("of your allocated storage. Consider reducing the allocated storage to match your actual needs.\n\n")
	}

	// Multi-AZ recommendation for production
	if !instance.MultiAZ && isProductionDatabase(instance) {
		recommendationCount++
		sb.WriteString(fmt.Sprintf("%d. **Enable Multi-AZ deployment** - ", recommendationCount))
		sb.WriteString("For production workloads, Multi-AZ is recommended to improve availability and reliability.\n\n")
	}

	// Reserved instance recommendation for long-running instances
	if !instance.LaunchTime.IsZero() {
		age := time.Since(instance.LaunchTime)
		if age.Hours()/24 > 90 { // Older than 90 days
			recommendationCount++
			sb.WriteString(fmt.Sprintf("%d. **Consider Reserved Instances** - ", recommendationCount))
			sb.WriteString("This instance has been running for over 90 days. If you plan to continue using it, ")
			sb.WriteString("purchasing a Reserved Instance could reduce costs by 30-60%.\n\n")
		}
	}

	// Default recommendation if none of the above
	if recommendationCount == 0 {
		sb.WriteString("1. **Monitor resource usage** - Continue monitoring performance metrics to identify optimization opportunities over time.\n\n")
	}

	return sb.String(), nil
}

// calculateRDSCO2Footprint estimates the carbon impact of an RDS instance
func calculateRDSCO2Footprint(instance RDSInstance) float64 {
	// Base factors for CO2 calculation
	const (
		// kg CO2 per vCPU-hour for different instance families
		standardCO2PerVCPUHour        = 0.0003
		memoryOptimizedCO2PerVCPUHour = 0.00035
		burstableCO2PerVCPUHour       = 0.00025

		// Storage impact
		storageCO2PerGBMonth = 0.00005

		// Multi-AZ multiplier (simplified)
		multiAZMultiplier = 1.8 // Not exactly 2x due to shared components
	)

	// Extract instance family and size from instance type
	// e.g., "db.m5.large" -> family="m5", size="large"
	instanceType := instance.InstanceType
	parts := strings.Split(instanceType, ".")
	if len(parts) < 3 {
		// Default to standard if we can't parse
		return 0.3 // Default estimate
	}

	family := parts[1]
	size := parts[2]

	// Estimate vCPUs based on instance type
	vCPUs := estimateVCPUs(family, size)

	// Select CO2 factor based on instance family
	co2PerVCPUHour := standardCO2PerVCPUHour
	if strings.HasPrefix(family, "m") || strings.HasPrefix(family, "r") || strings.HasPrefix(family, "x") {
		co2PerVCPUHour = memoryOptimizedCO2PerVCPUHour
	} else if strings.HasPrefix(family, "t") {
		co2PerVCPUHour = burstableCO2PerVCPUHour
	}

	// Calculate monthly vCPU hours
	vcpuHoursPerMonth := float64(vCPUs) * 730 // 730 hours in an average month

	// Calculate instance CO2
	instanceCO2 := vcpuHoursPerMonth * co2PerVCPUHour

	// Add storage impact
	storageCO2 := float64(instance.AllocatedStorage) * storageCO2PerGBMonth

	// Apply multi-AZ factor if enabled
	totalCO2 := instanceCO2 + storageCO2
	if instance.MultiAZ {
		totalCO2 *= multiAZMultiplier
	}

	return totalCO2
}

// estimateVCPUs provides a rough estimate of vCPUs based on instance family and size
func estimateVCPUs(family, size string) int {
	// This is a simplified mapping and would need to be expanded for production use
	switch size {
	case "nano":
		return 1
	case "micro":
		return 1
	case "small":
		return 1
	case "medium":
		return 2
	case "large":
		return 2
	case "xlarge":
		return 4
	case "2xlarge":
		return 8
	case "4xlarge":
		return 16
	case "8xlarge":
		return 32
	case "16xlarge":
		return 64
	default:
		// Extract number from size if it contains digits
		if strings.Contains(size, "xlarge") {
			// Try to parse "NxLarge" format
			sizeMultiplier := 1
			parts := strings.Split(size, "xlarge")
			if len(parts) > 0 && parts[0] != "" {
				if num, err := fmt.Sscanf(parts[0], "%d", &sizeMultiplier); err == nil && num > 0 {
					return sizeMultiplier * 4 // Assuming 4 vCPUs per xlarge
				}
			}
		}
		// Default to 2 if we can't determine
		return 2
	}
}

// estimateRDSCosts calculates current and potential optimized costs
func estimateRDSCosts(instance RDSInstance) (currentCost, optimizedCost float64) {
	// Note: This is a simplified cost model and should be replaced with more accurate pricing
	// in a production environment, potentially calling the AWS Price List API

	// Base hourly cost by instance type (on-demand pricing, approximated)
	instanceHourlyCost := estimateInstanceHourlyCost(instance.InstanceType, instance.Engine)

	// Adjust for Multi-AZ
	if instance.MultiAZ {
		instanceHourlyCost *= 2
	}

	// Storage cost
	var storageGB float64 = float64(instance.AllocatedStorage)
	storageCostPerGBMonth := 0.1 // Base assumption, adjust per type

	switch instance.StorageType {
	case "gp2", "gp3":
		storageCostPerGBMonth = 0.115
	case "io1":
		storageCostPerGBMonth = 0.125
		// Add provisioned IOPS cost if available
	case "standard":
		storageCostPerGBMonth = 0.1
	}

	// Calculate monthly costs
	instanceMonthlyCost := instanceHourlyCost * 730 // 730 hours in average month
	storageMonthlyCost := storageGB * storageCostPerGBMonth

	// Current total monthly cost
	currentCost = instanceMonthlyCost + storageMonthlyCost

	// Estimate optimized cost based on usage patterns
	optimizedCost = currentCost

	// If CPU utilization is very low, suggest a smaller instance
	if instance.CPUAvg7d < 5 {
		// Suggest going down two sizes (e.g., 2xlarge -> large)
		optimizedCost = (instanceMonthlyCost * 0.4) + storageMonthlyCost
	} else if instance.CPUAvg7d < 20 {
		// Suggest going down one size (e.g., 2xlarge -> xlarge)
		optimizedCost = (instanceMonthlyCost * 0.6) + storageMonthlyCost
	}

	// If storage is overprovisioned
	if instance.StorageUsed < 30 && instance.AllocatedStorage > 100 {
		// Estimate storage that would be needed (50% buffer over current usage)
		neededStorageGB := (instance.StorageUsed / 100.0) * storageGB * 1.5
		if neededStorageGB < 20 {
			neededStorageGB = 20 // Minimum storage
		}

		optimizedStorageCost := neededStorageGB * storageCostPerGBMonth
		optimizedCost = (optimizedCost - storageMonthlyCost) + optimizedStorageCost
	}

	// Factor in reserved instance savings for long-running instances
	if !instance.LaunchTime.IsZero() {
		age := time.Since(instance.LaunchTime)
		if age.Hours()/24 > 90 { // Older than 90 days
			// Apply approximate RI discount
			riDiscountFactor := 0.6 // 40% discount
			optimizedCost = (optimizedCost - (instanceMonthlyCost * (1.0 - riDiscountFactor)))
		}
	}

	// Don't allow optimized cost to be negative or greater than current
	if optimizedCost < 0 {
		optimizedCost = 0
	} else if optimizedCost > currentCost {
		// This shouldn't happen but just in case our algorithm has an issue
		optimizedCost = currentCost * 0.9
	}

	return currentCost, optimizedCost
}

// estimateInstanceHourlyCost provides a rough cost estimate for RDS instance types
func estimateInstanceHourlyCost(instanceType, engine string) float64 {
	// Simplified pricing model (approximations)
	// In a production system, use AWS Price List API for accurate pricing

	// Extract instance class and size
	parts := strings.Split(instanceType, ".")
	if len(parts) < 3 {
		return 0.2 // Default if we can't parse
	}

	family := parts[1]
	size := parts[2]

	// Base costs per vCPU hour (approximations)
	var baseCost float64

	switch {
	case strings.HasPrefix(family, "t"):
		baseCost = 0.02 // t family is burstable and cheaper
	case strings.HasPrefix(family, "m"):
		baseCost = 0.05 // m family is general purpose
	case strings.HasPrefix(family, "r"):
		baseCost = 0.06 // r family is memory optimized
	case strings.HasPrefix(family, "c"):
		baseCost = 0.04 // c family is compute optimized
	default:
		baseCost = 0.05 // Default
	}

	// Adjust for engine type
	switch {
	case strings.Contains(engine, "mysql") || strings.Contains(engine, "mariadb"):
		// MySQL/MariaDB pricing is often the baseline
	case strings.Contains(engine, "postgres"):
		baseCost *= 1.1 // PostgreSQL slightly more expensive
	case strings.Contains(engine, "sqlserver"):
		baseCost *= 1.6 // SQL Server significantly more expensive
	case strings.Contains(engine, "oracle"):
		baseCost *= 2.0 // Oracle most expensive
	}

	// Multiply by vCPUs to get instance cost
	vCPUs := estimateVCPUs(family, size)

	return baseCost * float64(vCPUs)
}

// calculateRDSOptimizationScore produces a 0-100 score for optimization potential
func calculateRDSOptimizationScore(instance RDSInstance, savingsPct float64) int {
	score := 0

	// Base score from potential savings percentage
	// 0% savings = 0 points, 50% or higher savings = 50 points
	score += int(math.Min(savingsPct, 50))

	// Low CPU utilization: add up to 20 points
	if instance.CPUAvg7d < 5 {
		score += 20
	} else if instance.CPUAvg7d < 10 {
		score += 15
	} else if instance.CPUAvg7d < 20 {
		score += 10
	}

	// Storage overprovisioning: add up to 15 points
	if instance.StorageUsed < 20 {
		score += 15
	} else if instance.StorageUsed < 40 {
		score += 10
	} else if instance.StorageUsed < 60 {
		score += 5
	}

	// Non-reserved long-running instance: add up to 15 points
	if !instance.LaunchTime.IsZero() {
		age := time.Since(instance.LaunchTime)
		ageInDays := age.Hours() / 24

		if ageInDays > 180 { // Older than 6 months
			score += 15
		} else if ageInDays > 90 { // Older than 3 months
			score += 10
		} else if ageInDays > 30 { // Older than 1 month
			score += 5
		}
	}

	// Cap at 100
	if score > 100 {
		score = 100
	}

	return score
}

// isProductionDatabase attempts to determine if an RDS instance is for production use
func isProductionDatabase(instance RDSInstance) bool {
	// Check instance size - larger instances more likely to be production
	isSizeProduction := false
	if strings.HasSuffix(instance.InstanceType, "large") ||
		strings.HasSuffix(instance.InstanceType, "xlarge") ||
		strings.Contains(instance.InstanceType, "2xlarge") ||
		strings.Contains(instance.InstanceType, "4xlarge") ||
		strings.Contains(instance.InstanceType, "8xlarge") ||
		strings.Contains(instance.InstanceType, "16xlarge") {
		isSizeProduction = true
	}

	// Check tags
	isTaggedProduction := false
	for k, v := range instance.Tags {
		lowerKey := strings.ToLower(k)
		lowerValue := strings.ToLower(v)

		if (lowerKey == "environment" || lowerKey == "env") &&
			(lowerValue == "prod" || lowerValue == "production") {
			isTaggedProduction = true
			break
		}

		if strings.Contains(lowerValue, "prod") ||
			strings.Contains(lowerValue, "production") {
			isTaggedProduction = true
			break
		}
	}

	// Check instance name
	isNamedProduction := strings.Contains(strings.ToLower(instance.InstanceID), "prod")

	// Instance is considered production if at least two conditions are met
	productionSignals := 0
	if isSizeProduction {
		productionSignals++
	}
	if isTaggedProduction {
		productionSignals++
	}
	if isNamedProduction {
		productionSignals++
	}

	return productionSignals >= 2
}
