package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
func AnalyzeInstance(ctx context.Context, client *bedrockruntime.Client, invocationID, recordJSON string, cpuAvg float64) (string, error) {
	// Compose prompt
	prompt := fmt.Sprintf(`Here is an EC2 instance record:
%s

Metrics: 7-day average CPU utilization of %.1f%%.

1) Identify any inefficiencies (over-provisioning, idle time).
2) Estimate monthly CO2 footprint (0.0002 kg CO2 per vCPU-hour).
3) Suggest a rightsizing or shutdown action.
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

	// Parse Claude 3.7 response format (updated for newer models)
	var claudeResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &claudeResp); err == nil && len(claudeResp.Content) > 0 {
		var sb strings.Builder
		for _, content := range claudeResp.Content {
			if content.Type == "text" {
				sb.WriteString(content.Text)
			}
		}
		if sb.Len() > 0 {
			return sb.String(), nil
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
	if err := json.Unmarshal(data, &assistantResp); err == nil &&
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
			return sb.String(), nil
		}
	}

	// Try standard response patterns (existing code)
	var wrap map[string]interface{}
	if err := json.Unmarshal(data, &wrap); err == nil {
		// Titan legacy completion
		if c, ok := wrap["completion"].(string); ok {
			return c, nil
		}
		// Titan/Claude results array
		if results, ok := wrap["results"].([]interface{}); ok && len(results) > 0 {
			if entry, ok := results[0].(map[string]interface{}); ok {
				if text, ok := entry["outputText"].(string); ok {
					return text, nil
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
				return result, nil
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
									return txt, nil
								}
							}
						}
					}
				}
			}
		}
	}

	// Log an error before returning the raw response
	log.Printf("Warning: Unable to parse model response into expected format. Returning raw response.")
	return string(data), nil
}
