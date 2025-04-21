package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	// AWS SDK v2 modules for config and Bedrock runtime
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// EmbeddingResult holds the response from the Bedrock embedding model
// - ID: resource identifier
// - Embeddings: numeric vector representing the semantic content
type EmbeddingResult struct {
	ID         string    `json:"id"`
	Embeddings []float64 `json:"embeddings"`
}

// embedText calls Bedrock to get embeddings for the input text
// It handles both V2 and legacy embedding schemas, and attempts to
// extract the embedding vector from various possible response formats.
func EmbedText(ctx context.Context, client *bedrockruntime.Client, modelID, text string) ([]float64, error) {
	var body []byte
	var err error

	// Build request payload based on model version
	if strings.Contains(modelID, "titan-embed-text-v2") {
		// Titan Text Embeddings V2 expects a richer schema
		payload := map[string]interface{}{
			"inputText":  text,
			"dimensions": 512,
			"normalize":  true,
		}
		body, err = json.Marshal(payload)
	} else {
		// Legacy Titan Embedding schema
		payload := map[string]string{"input": text}
		body, err = json.Marshal(payload)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embed payload: %w", err)
	}

	// Invoke the embedding model on Bedrock
	resp, err := client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(modelID),
		ContentType: aws.String("application/json"),
		Body:        body,
	})
	if err != nil {
		return nil, fmt.Errorf("embed invoke error for model %s: %w", modelID, err)
	}

	// The SDK returns raw JSON bytes in resp.Body
	data := resp.Body
	log.Printf("Raw embedding response: %s", string(data))

	// 1) Try the simple path: top-level "embeddings" field
	var direct struct {
		Embeddings []float64 `json:"embeddings"`
	}
	if err := json.Unmarshal(data, &direct); err == nil && len(direct.Embeddings) > 0 {
		return direct.Embeddings, nil
	}

	// 2) Fallback: dynamic parsing for other common schemas
	var gen map[string]interface{}
	if err := json.Unmarshal(data, &gen); err != nil {
		return nil, fmt.Errorf("failed to parse embedding response JSON: %w", err)
	}

	// Helper to extract []float64 from []interface{}
	toFloat64Slice := func(arr []interface{}) []float64 {
		out := make([]float64, len(arr))
		for i, v := range arr {
			switch num := v.(type) {
			case float64:
				out[i] = num
			case json.Number:
				f, _ := num.Float64()
				out[i] = f
			}
		}
		return out
	}

	// Check for "embeddings" key
	if raw, ok := gen["embeddings"]; ok {
		if arr, ok := raw.([]interface{}); ok {
			return toFloat64Slice(arr), nil
		}
	}
	// Check for singular "embedding"
	if raw, ok := gen["embedding"]; ok {
		if arr, ok := raw.([]interface{}); ok {
			return toFloat64Slice(arr), nil
		}
	}
	// Check nested under "results"
	if r, ok := gen["results"]; ok {
		if list, ok := r.([]interface{}); ok && len(list) > 0 {
			if first, ok := list[0].(map[string]interface{}); ok {
				if raw, ok := first["embeddings"]; ok {
					if arr, ok := raw.([]interface{}); ok {
						return toFloat64Slice(arr), nil
					}
				}
				if raw, ok := first["embedding"]; ok {
					if arr, ok := raw.([]interface{}); ok {
						return toFloat64Slice(arr), nil
					}
				}
			}
		}
	}

	// If we reach here, we couldn't find an embedding vector
	return nil, fmt.Errorf("no embeddings found in response for model %s", modelID)
}
