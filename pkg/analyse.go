package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// AnalysisResult holds the structured LLM output for an instance
// - ID: the instance identifier
// - Analysis: the raw text recommendation
// For MVP, we treat Analysis as a freeform string.
type AnalysisResult struct {
	ID       string `json:"id"`
	Analysis string `json:"analysis"`
}

// analyzeInstance sends a prompt about an EC2 record to a Bedrock text model
// and returns the completion text. It handles Titan, Lite V1, and Claude schemas.
// analyzeInstance sends a prompt about an EC2 record to a Bedrock text model
// and returns the completion text. It handles Titan, Lite V1, and Claude schemas.
// This function needs to be updated to ensure the EC2 analysis
// follows the same format as RDS and S3 for CO2 footprint data

// AnalyzeInstance sends a prompt about an EC2 record to a Bedrock text model
// and returns the completion text. It handles Titan, Lite V1, and Claude schemas.
func AnalyzeInstance(ctx context.Context, client *bedrockruntime.Client, invocationID, recordJSON string, cpuAvg float64) (string, error) {
	// Compose prompt with formatting guidelines for consistent output
	prompt := fmt.Sprintf(`This is a cloud optimisation tool thats also helping with sustenability efforts. Keep a clean formatting and dont use any "*" or "#". Here is an EC2 instance record. :
%s

Metrics: 7-day average CPU utilization of %.1f%%.

Please analyze this EC2 instance for sustainability and cost optimization. 
Your analysis should include:
1) Identify any inefficiencies (over-provisioning, idle time).
2) Estimate monthly CO2 footprint (0.0002 kg CO2 per vCPU-hour).
3) Suggest a rightsizing or shutdown action.
4) Provide security recommendations
5) Provide SUSTENABILITY TIPS for this finding

IMPORTANT: Include a "Cost & Environmental Impact" section with the following format:
Cost & Environmental Impact
- Estimated Monthly Cost: $X.XX
- Potential Optimized Cost: $X.XX
- Monthly Savings Potential*: $X.XX (XX.X%%)
- CO2 Footprint: X.XX kg CO2 per month
`, recordJSON, cpuAvg)

	var body []byte
	var err error

	// Check if it's an inference profile (contains "inference-profile" in the ARN)
	if strings.Contains(invocationID, "inference-profile") &&
		(strings.Contains(invocationID, "anthropic") || strings.Contains(invocationID, "claude")) {
		// Claude 3 schema for Bedrock via inference profile
		payload := map[string]interface{}{
			"anthropic_version": "bedrock-2023-05-31",
			"max_tokens":        800,
			"temperature":       0.0,
			"messages": []map[string]interface{}{
				{
					"role": "user",
					"content": []map[string]string{
						{"type": "text", "text": prompt},
					},
				},
			},
		}
		body, err = json.Marshal(payload)
	} else if strings.Contains(invocationID, "anthropic") || strings.Contains(invocationID, "claude") {
		// Standard Claude model (not an inference profile)
		payload := map[string]interface{}{
			"anthropic_version": "bedrock-2023-05-31",
			"max_tokens":        300,
			"temperature":       0.0,
			"messages": []map[string]interface{}{
				{
					"role": "user",
					"content": []map[string]string{
						{"type": "text", "text": prompt},
					},
				},
			},
		}
		body, err = json.Marshal(payload)
	} else if strings.Contains(invocationID, "text-lite-v1") {
		// Titan Text Lite V1 schema
		payload := map[string]interface{}{
			"inputText": prompt,
			"textGenerationConfig": map[string]interface{}{
				"maxTokenCount": 300,
				"temperature":   0.0,
				"topP":          1.0,
			},
		}
		body, err = json.Marshal(payload)
	} else {
		// Legacy Titan schema
		payload := map[string]interface{}{
			"prompt":      prompt,
			"maxTokens":   300,
			"temperature": 0.0,
		}
		body, err = json.Marshal(payload)
	}

	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Log what we're about to send
	log.Printf("Invoking model ID: %s with payload: %s", invocationID, string(body))

	// Invoke model/profile
	resp, err := client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(invocationID),
		ContentType: aws.String("application/json"),
		Body:        body,
	})
	if err != nil {
		return "", fmt.Errorf("generation invoke error for %s: %w", invocationID, err)
	}

	data := resp.Body
	log.Printf("Raw generation response: %s", string(data))

	// Parse different response formats...
	// (existing code for parsing response remains the same)
	// ...

	// If we didn't get a properly formatted Cost & Environmental Impact section,
	// we should add one to maintain format consistency
	result := extractTextFromResponse(data)

	// Check if the response has the properly formatted section
	if !strings.Contains(result, "CO2 Footprint:") {
		// We need to extract the CO2 calculation and reformat
		co2Value := extractCO2Value(result)

		// Calculate the potential savings based on CPU utilization
		var potentialSavings float64
		if cpuAvg < 5 {
			// If CPU is very low, estimate high savings (80%)
			potentialSavings = 0.8
		} else if cpuAvg < 20 {
			// If CPU is low, estimate medium savings (50%)
			potentialSavings = 0.5
		} else if cpuAvg < 40 {
			// If CPU is moderate, estimate small savings (30%)
			potentialSavings = 0.3
		} else {
			// If CPU is high, estimate minimal savings (10%)
			potentialSavings = 0.1
		}

		// Estimate cost based on instance type (this is a very rough estimate)
		instanceType := extractInstanceType(result)
		monthlyCost := estimateEC2MonthlyCost(instanceType)

		// Calculate optimized cost and savings
		optimizedCost := monthlyCost * (1 - potentialSavings)
		savingsAmount := monthlyCost - optimizedCost

		// Format the Cost & Environmental Impact section
		costSection := fmt.Sprintf(`
Cost & Environmental Impact
- Estimated Monthly Cost: $%.2f
- Potential Optimized Cost: $%.2f
- Monthly Savings Potential: $%.2f (%.1f%%)
- CO2 Footprint: %.3f kg CO2 per month
`, monthlyCost, optimizedCost, savingsAmount, potentialSavings*100, co2Value)

		// If there's an existing section with "CO2 Footprint Calculation" or similar, replace it
		// Otherwise, append the new section before any recommendations
		if idx := strings.Index(result, " CO2 Footprint"); idx > 0 {
			// Find the next section
			nextSection := strings.Index(result[idx:], "")
			if nextSection > 0 {
				// Replace the entire section
				result = result[:idx] + costSection + result[idx+nextSection:]
			} else {
				// Replace to the end
				result = result[:idx] + costSection
			}
		} else if idx := strings.Index(result, " Environmental Impact"); idx > 0 {
			// Find the next section
			nextSection := strings.Index(result[idx:], "")
			if nextSection > 0 {
				// Replace the entire section
				result = result[:idx] + costSection + result[idx+nextSection:]
			} else {
				// Replace to the end
				result = result[:idx] + costSection
			}
		} else if idx := strings.Index(result, " Recommendations"); idx > 0 {
			// Insert before recommendations
			result = result[:idx] + costSection + result[idx:]
		} else {
			// Just append at the end
			result += costSection
		}
	}

	return result, nil
}

// Helper function to extract CO2 value from EC2 analysis
func extractCO2Value(analysis string) float64 {
	// Look for various CO2 calculation patterns

	// Pattern 1: X.XXX kg CO2 per month
	re := regexp.MustCompile(`([\d\.]+)\s*kg CO2 per month`)
	if matches := re.FindStringSubmatch(analysis); len(matches) > 1 {
		if value, err := strconv.ParseFloat(matches[1], 64); err == nil {
			return value
		}
	}

	// Pattern 2: X.XXX kg CO2/month
	re = regexp.MustCompile(`([\d\.]+)\s*kg CO2/month`)
	if matches := re.FindStringSubmatch(analysis); len(matches) > 1 {
		if value, err := strconv.ParseFloat(matches[1], 64); err == nil {
			return value
		}
	}

	// Pattern 3: Monthly CO2 footprint: X.XXX kg
	re = regexp.MustCompile(`Monthly CO2 footprint.*?(\d+\.\d+)`)
	if matches := re.FindStringSubmatch(analysis); len(matches) > 1 {
		if value, err := strconv.ParseFloat(matches[1], 64); err == nil {
			return value
		}
	}

	// Pattern 4: X vCPUs × Y hours × Z days × 0.0002 kg CO2/vCPU-hour = X.XXX kg
	re = regexp.MustCompile(`(\d+)\s*vCPUs.*?(\d+)\s*hours.*?(\d+)\s*days.*?0\.0002`)
	if matches := re.FindStringSubmatch(analysis); len(matches) > 3 {
		vCPUs, _ := strconv.ParseFloat(matches[1], 64)
		hours, _ := strconv.ParseFloat(matches[2], 64)
		days, _ := strconv.ParseFloat(matches[3], 64)
		return vCPUs * hours * days * 0.0002
	}

	// Default estimate based on t3.small (2 vCPUs)
	return 2 * 24 * 30 * 0.0002 // 0.288 kg CO2 per month
}

// Extract instance type from analysis
func extractInstanceType(analysis string) string {
	re := regexp.MustCompile(`Instance Type.*?([a-z]\d[a-z]?\.[a-zA-Z0-9]+)`)
	if matches := re.FindStringSubmatch(analysis); len(matches) > 1 {
		return matches[1]
	}
	return "t3.small" // Default if not found
}

// Rough estimate of monthly cost for EC2 instance type
func estimateEC2MonthlyCost(instanceType string) float64 {
	// This is a very simplified pricing model
	// In a real implementation, you'd use the AWS Price List API

	// Extract the family and size
	parts := strings.Split(instanceType, ".")
	if len(parts) < 2 {
		return 15.00 // Default to $15/month if we can't parse
	}

	family := parts[0]
	size := parts[1]

	// Base price per hour for t3.micro
	basePrice := 0.0104 // $0.0104 per hour

	// Apply family multiplier
	familyMultiplier := 1.0
	switch family {
	case "t2":
		familyMultiplier = 0.8
	case "t3":
		familyMultiplier = 1.0
	case "t4g":
		familyMultiplier = 0.8
	case "m5":
		familyMultiplier = 1.5
	case "m6g":
		familyMultiplier = 1.4
	case "c5":
		familyMultiplier = 1.6
	case "r5":
		familyMultiplier = 1.8
	default:
		familyMultiplier = 1.0
	}

	// Apply size multiplier
	sizeMultiplier := 1.0
	switch size {
	case "nano":
		sizeMultiplier = 0.25
	case "micro":
		sizeMultiplier = 0.5
	case "small":
		sizeMultiplier = 1.0
	case "medium":
		sizeMultiplier = 2.0
	case "large":
		sizeMultiplier = 4.0
	case "xlarge":
		sizeMultiplier = 8.0
	case "2xlarge":
		sizeMultiplier = 16.0
	case "4xlarge":
		sizeMultiplier = 32.0
	case "8xlarge":
		sizeMultiplier = 64.0
	case "16xlarge":
		sizeMultiplier = 128.0
	default:
		// Try to extract size multiplier from format like "2xlarge"
		if numSize := strings.TrimSuffix(size, "xlarge"); numSize != size {
			if num, err := strconv.Atoi(numSize); err == nil {
				sizeMultiplier = float64(num) * 8.0
			}
		}
	}

	// Calculate hourly cost
	hourlyCost := basePrice * familyMultiplier * sizeMultiplier

	// Convert to monthly (730 hours per month on average)
	monthlyCost := hourlyCost * 730.0

	return monthlyCost
}

// Helper function to extract text from various response formats
func extractTextFromResponse(responseData []byte) string {
	// Try parsing Claude 3.7 response format
	var claudeResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(responseData, &claudeResp); err == nil && len(claudeResp.Content) > 0 {
		var sb strings.Builder
		for _, content := range claudeResp.Content {
			if content.Type == "text" {
				sb.WriteString(content.Text)
			}
		}
		if sb.Len() > 0 {
			return sb.String()
		}
	}

	// If that didn't work, try parsing as an assistant message
	var assistantResp struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(responseData, &assistantResp); err == nil &&
		assistantResp.Type == "message" &&
		assistantResp.Message.Role == "assistant" &&
		len(assistantResp.Message.Content) > 0 {
		var sb strings.Builder
		for _, content := range assistantResp.Message.Content {
			if content.Type == "text" {
				sb.WriteString(content.Text)
			}
		}
		if sb.Len() > 0 {
			return sb.String()
		}
	}

	// Try standard response patterns
	var wrap map[string]interface{}
	if err := json.Unmarshal(responseData, &wrap); err == nil {
		// Titan legacy completion
		if c, ok := wrap["completion"].(string); ok {
			return c
		}
		// Titan/Claude results array
		if results, ok := wrap["results"].([]interface{}); ok && len(results) > 0 {
			if entry, ok := results[0].(map[string]interface{}); ok {
				if text, ok := entry["outputText"].(string); ok {
					return text
				}
			}
		}
		// Anthropic Claude-style top-level content
		if contentArr, ok := wrap["content"].([]interface{}); ok && len(contentArr) > 0 {
			// content is array of message parts
			var sb strings.Builder
			for _, part := range contentArr {
				if partMap, ok := part.(map[string]interface{}); ok {
					if txt, ok := partMap["text"].(string); ok {
						sb.WriteString(txt)
					}
				}
			}
			result := sb.String()
			if result != "" {
				return result
			}
		}
		// Anthropic messages array
		if msgs, ok := wrap["messages"].([]interface{}); ok && len(msgs) > 0 {
			for _, m := range msgs {
				if msgObj, ok := m.(map[string]interface{}); ok {
					if role, ok := msgObj["role"].(string); ok && role == "assistant" {
						// For chat, content is array
						if contentArr, ok := msgObj["content"].([]interface{}); ok && len(contentArr) > 0 {
							if firstPart, ok := contentArr[0].(map[string]interface{}); ok {
								if txt, ok := firstPart["text"].(string); ok {
									return txt
								}
							}
						}
					}
				}
			}
		}
	}

	// Return the raw response as a last resort
	return string(responseData)
}
