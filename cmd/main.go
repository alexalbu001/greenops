package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	// Import the shared GreenOps library
	pkg "github.com/alexalbu001/greenops/pkg"
)

// ReportItem combines instance metadata, embedding vector, and LLM analysis
type ReportItem struct {
	Instance  pkg.Instance `json:"instance"`
	Embedding []float64    `json:"embedding"`
	Analysis  string       `json:"analysis"`
}

func main() {
	ctx := context.Background()

	// Load AWS configuration
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("unable to load AWS config: %v", err)
	}

	// AWS service clients
	ec2Client := ec2.NewFromConfig(cfg)
	cwClient := cloudwatch.NewFromConfig(cfg)
	brClient := bedrockruntime.NewFromConfig(cfg)

	// Determine embedding and generation models
	embedModel := os.Getenv("EMBED_MODEL_ID")
	if embedModel == "" {
		embedModel = "amazon.titan-embed-text-v2:0"
	}
	genID := os.Getenv("GEN_PROFILE_ARN")
	if genID == "" {
		genID = os.Getenv("GEN_MODEL_ID")
		if genID == "" {
			genID = "amazon.titan-tg1-large"
		}
	}

	// Collect EC2 instances and metrics
	instances, err := pkg.ListInstances(ctx, ec2Client, cwClient)
	if err != nil {
		log.Fatalf("collection error: %v", err)
	}

	var report []ReportItem
	for _, inst := range instances {
		// Serialize the instance record to JSON
		recJSON, _ := json.Marshal(inst)
		record := string(recJSON)

		// Generate embedding vector
		embedding, err := pkg.EmbedText(ctx, brClient, embedModel, record)
		if err != nil {
			log.Printf("warn: embedding failed for %s: %v", inst.InstanceID, err)
			continue
		}

		// Perform LLM analysis
		analysis, err := pkg.AnalyzeInstance(ctx, brClient, genID, record, inst.CPUAvg7d)
		if err != nil {
			log.Printf("warn: analysis failed for %s: %v", inst.InstanceID, err)
		}

		report = append(report, ReportItem{
			Instance:  inst,
			Embedding: embedding,
			Analysis:  analysis,
		})
	}

	// Print the aggregated report as JSON
	out, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(out))
}
