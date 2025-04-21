package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	pkg "github.com/alexalbu001/greenops/pkg"
)

// ServerRequest represents incoming payload of instances to analyze
type ServerRequest struct {
	Instances []pkg.Instance `json:"instances"`
}

// ReportItem for a single instance
type ReportItem struct {
	Instance  pkg.Instance `json:"instance"`
	Embedding []float64    `json:"embedding"`
	Analysis  string       `json:"analysis"`
}

// Handler is the Lambda entrypoint
// Handler is the Lambda entrypoint
func Handler(ctx context.Context, apiReq events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	log.Printf("Received event: %s", apiReq.Body)

	var req ServerRequest
	if err := json.Unmarshal([]byte(apiReq.Body), &req); err != nil {
		log.Printf("invalid request payload: %v", err)
		return events.APIGatewayV2HTTPResponse{
			StatusCode: 400,
			Body:       `{"error":"invalid JSON payload"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	// Validate request
	if len(req.Instances) == 0 {
		log.Printf("request contained no instances to analyze")
		return events.APIGatewayV2HTTPResponse{
			StatusCode: 400,
			Body:       `{"error":"no instances provided in request"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	// Load AWS config for Bedrock
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Printf("unable to load AWS config: %v", err)
		return events.APIGatewayV2HTTPResponse{
			StatusCode: 500,
			Body:       `{"error":"failed to initialize AWS client"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}
	brClient := bedrockruntime.NewFromConfig(cfg)

	// Model IDs from env with logging
	embedModel := os.Getenv("EMBED_MODEL_ID")
	if embedModel == "" {
		embedModel = "amazon.titan-embed-text-v2:0"
	}
	log.Printf("Using embedding model: %s", embedModel)

	genID := os.Getenv("GEN_PROFILE_ARN")
	if genID == "" {
		genID = os.Getenv("GEN_MODEL_ID")
		if genID == "" {
			genID = "arn:aws:bedrock:eu-west-1:767048271788:inference-profile/eu.anthropic.claude-3-7-sonnet-20250219-v1:0"
		}
	}
	log.Printf("Using generation model/profile: %s", genID)

	// Build report with proper error handling
	var report []ReportItem
	var processingErrors []string

	for _, inst := range req.Instances {
		data, err := json.Marshal(inst)
		if err != nil {
			errMsg := fmt.Sprintf("failed to marshal instance %s: %v", inst.InstanceID, err)
			log.Printf(errMsg)
			processingErrors = append(processingErrors, errMsg)
			continue
		}
		record := string(data)

		// Embedding phase
		emb, err := pkg.EmbedText(ctx, brClient, embedModel, record)
		if err != nil {
			errMsg := fmt.Sprintf("embed error for %s: %v", inst.InstanceID, err)
			log.Printf(errMsg)
			processingErrors = append(processingErrors, errMsg)
			continue
		}

		// Analysis phase
		analysis, err := pkg.AnalyzeInstance(ctx, brClient, genID, record, inst.CPUAvg7d)
		if err != nil {
			errMsg := fmt.Sprintf("analysis error for %s: %v", inst.InstanceID, err)
			log.Printf(errMsg)
			processingErrors = append(processingErrors, errMsg)
			// Still add the item to the report but with an error message as analysis
			analysis = fmt.Sprintf("ERROR: Failed to analyze instance: %v", err)
		}

		// Add successfully processed item to report
		report = append(report, ReportItem{inst, emb, analysis})
	}

	// Check if we have anything to return
	if len(report) == 0 {
		log.Printf("no instances were successfully processed")
		return events.APIGatewayV2HTTPResponse{
			StatusCode: 500,
			Body: fmt.Sprintf(`{"error":"failed to process any instances","details":%s}`,
				mustMarshalJSON(processingErrors)),
			Headers: map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	// Construct response with proper error handling
	respData := map[string]interface{}{
		"report": report,
	}

	// Add errors if any occurred
	if len(processingErrors) > 0 {
		respData["warnings"] = processingErrors
	}

	respBody, err := json.Marshal(respData)
	if err != nil {
		log.Printf("failed to marshal response: %v", err)
		return events.APIGatewayV2HTTPResponse{
			StatusCode: 500,
			Body:       `{"error":"internal server error - failed to construct response"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	return events.APIGatewayV2HTTPResponse{
		StatusCode:      200,
		Headers:         map[string]string{"Content-Type": "application/json"},
		Body:            string(respBody),
		IsBase64Encoded: false,
	}, nil
}

// Helper function to ensure JSON marshaling doesn't fail
func mustMarshalJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`"[marshaling error: %s]"`, err)
	}
	return string(data)
}

func main() {
	lambda.Start(Handler)
}
