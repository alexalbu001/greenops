package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// AnalysisResult holds the structured LLM output for an instance
// - ID: the instance identifier
// - Analysis: the raw text recommendation
type AnalysisResult struct {
	ID       string `json:"id"`
	Analysis string `json:"analysis"`
}

// InvokeBedrockModel is a general-purpose function for sending prompts to any Bedrock model
// and handling the various response formats consistently
func InvokeBedrockModel(ctx context.Context, client *bedrockruntime.Client, modelID string, prompt string) (string, error) {
	var body []byte
	var err error

	// Check if it's an inference profile (contains "inference-profile" in the ARN)
	if strings.Contains(modelID, "inference-profile") &&
		(strings.Contains(modelID, "anthropic") || strings.Contains(modelID, "claude")) {
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
	} else if strings.Contains(modelID, "anthropic") || strings.Contains(modelID, "claude") {
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
	} else if strings.Contains(modelID, "text-lite-v1") {
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
	log.Printf("Invoking model ID: %s with payload length: %d bytes", modelID, len(body))
	if len(body) < 1000 { // Only log full payload if it's small
		log.Printf("Payload: %s", string(body))
	}

	// Invoke model/profile
	resp, err := client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(modelID),
		ContentType: aws.String("application/json"),
		Body:        body,
	})
	if err != nil {
		return "", fmt.Errorf("generation invoke error for %s: %w", modelID, err)
	}

	data := resp.Body
	log.Printf("Received response with length: %d bytes", len(data))

	// Extract the text response
	result := extractTextFromResponse(data)
	return result, nil
}

// AnalyzeInstance sends a prompt about an EC2 record to a Bedrock text model
// and returns the completion text.
func AnalyzeInstance(ctx context.Context, client *bedrockruntime.Client, modelID string, recordJSON string, cpuAvg float64) (string, error) {
	// Compose prompt with formatting guidelines for consistent output
	prompt := fmt.Sprintf(`This is a cloud optimisation tool called GreenOps that's also helping with sustainability efforts. Here is an EC2 instance record:
%s

Metrics: 7-day average CPU utilization of %.1f%%.

Please analyze this EC2 instance for sustainability and cost optimization. 
Your analysis must include:
1) Calculate monthly CO2 footprint using the formula: vCPUs × 24 hours × 30 days × 0.0002 kg CO2/vCPU-hour
2) Estimate monthly cost based on the instance type and region
3) Calculate potential cost and CO2 savings if the instance was rightsized or optimized
4) Identify any inefficiencies (over-provisioning, idle time)
5) Suggest specific rightsizing or shutdown actions
6) Provide security recommendations
7) Provide SUSTAINABILITY TIPS for this finding

FOLLOW THIS EXACT FORMAT FOR YOUR ANALYSIS:

# EC2 Instance Analysis: [INSTANCE_ID]

## Performance Metrics
- CPU Utilization (7-day avg): [PERCENTAGE]%
- [OTHER METRICS IF AVAILABLE]

## Analysis

[1-2 paragraphs general description]

### Inefficiencies Identified

1. [ISSUE 1]: [DESCRIPTION]
2. [ISSUE 2]: [DESCRIPTION]
3. [ISSUE 3]: [DESCRIPTION]

## Recommendations

1. [CATEGORY 1]:
   - [ACTION ITEM]
   - [ACTION ITEM]

2. [CATEGORY 2]:
   - [ACTION ITEM]
   - [ESTIMATED IMPACT]

## Cost & Environmental Impact
- Estimated Monthly Cost: $X.XX
- Potential Optimized Cost: $X.XX
- Monthly Savings Potential: $X.XX (XX.X%)
- CO2 Footprint: X.XX kg CO2 per month

## Security Considerations

1. [SECURITY ITEM 1]: [DESCRIPTION]
2. [SECURITY ITEM 2]: [DESCRIPTION]

## Sustainability Tips

1. [TIP 1]: [DESCRIPTION]
2. [TIP 2]: [DESCRIPTION]
3. [TIP 3]: [DESCRIPTION]
`, recordJSON, cpuAvg)

	// Use the general-purpose function to invoke Bedrock
	result, err := InvokeBedrockModel(ctx, client, modelID, prompt)
	if err != nil {
		return "", err
	}

	return result, nil
}

// Extract instance type from analysis
func extractInstanceType(analysis string) string {
	re := regexp.MustCompile(`Instance Type.*?([a-z]\d[a-z]?\.[a-zA-Z0-9]+)`)
	if matches := re.FindStringSubmatch(analysis); len(matches) > 1 {
		return matches[1]
	}
	return "t3.small" // Default if not found
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
