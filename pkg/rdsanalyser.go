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

	// Construct the prompt with an example to ensure consistent formatting
	prompt := fmt.Sprintf(`Here is an RDS instance record. This is a cloud optimisation tool that's also helping with sustainability efforts:
%s

Please analyze this RDS instance for sustainability and cost optimization.
Your analysis must include:
1) Calculate the monthly CO2 footprint considering database instance family, size, and Multi-AZ
2) Estimate monthly cost based on the instance type, storage, and settings
3) Identify inefficiencies (over-provisioning, low utilization, etc.)
4) Calculate potential savings from rightsizing or optimization
5) Suggest specific actions for rightsizing or optimization
6) Identify any performance or availability concerns
7) Provide SUSTAINABILITY TIPS for this finding

FOLLOW THIS EXACT FORMAT FOR YOUR ANALYSIS:

# RDS Instance Analysis: [INSTANCE_ID]

## Performance Metrics
- CPU Utilization (7-day avg): [PERCENTAGE]%
- Database Connections (7-day avg): [NUMBER]
- IOPS (7-day avg): [NUMBER]
- Storage Used: [PERCENTAGE]%

## Analysis

[1-2 paragraphs general description]

### Inefficiencies Identified

1. [ISSUE 1]: [DESCRIPTION]
2. [ISSUE 2]: [DESCRIPTION]
3. [ISSUE 3]: [DESCRIPTION]

### Optimization Recommendations

1. [RECOMMENDATION 1]: [DESCRIPTION]
2. [RECOMMENDATION 2]: [DESCRIPTION]
3. [RECOMMENDATION 3]: [DESCRIPTION]

## Cost & Environmental Impact
- Estimated Monthly Cost: $X.XX
- Potential Optimized Cost: $X.XX
- Monthly Savings Potential: $X.XX (XX.X%)
- CO2 Footprint: X.XX kg CO2 per month

## Sustainability Tips

1. [TIP 1]: [DESCRIPTION]
2. [TIP 2]: [DESCRIPTION]
3. [TIP 3]: [DESCRIPTION]
`, instanceJSON)

	// Use the general-purpose function to invoke Bedrock
	analysis, err := InvokeBedrockModel(ctx, client, modelID, prompt)
	if err != nil {
		return "", err
	}

	return analysis, nil
}

// AnalyzeRDSInstance generates optimization recommendations for a single RDS instance using Bedrock
func AnalyzeRDSInstance(ctx context.Context, instance RDSInstance, client *bedrockruntime.Client, modelID string) (RDSInstanceAnalysis, error) {
	analysis := RDSInstanceAnalysis{
		Instance: instance,
	}

	// Get embeddings
	embeddings, err := EmbedText(ctx, client, "amazon.titan-embed-text-v2:0", instance.InstanceID)
	if err != nil {
		return analysis, err
	}
	analysis.Embedding = embeddings

	// Get analysis directly from Bedrock
	analysisText, err := AnalyzeRDSInstanceWithBedrock(ctx, client, modelID, instance, embeddings)
	if err != nil {
		return analysis, err
	}
	analysis.Analysis = analysisText

	// Extract cost and CO2 metrics from the Bedrock response
	extractRDSMetricsFromAnalysis(&analysis)

	return analysis, nil
}

// Helper function to extract metrics from the Bedrock analysis text
func extractRDSMetricsFromAnalysis(analysis *RDSInstanceAnalysis) {
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
