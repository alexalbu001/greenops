// Create cmd/worker/main.go

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
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	pkg "github.com/alexalbu001/greenops/pkg"
)

func Handler(ctx context.Context, sqsEvent events.SQSEvent) error {
	// Load AWS config
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Printf("unable to load AWS config: %v", err)
		return fmt.Errorf("unable to load AWS config: %v", err)
	}

	// Create clients
	dynamoClient := dynamodb.NewFromConfig(cfg)
	brClient := bedrockruntime.NewFromConfig(cfg)

	// Get model IDs
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

	// Process each message in the batch
	for _, record := range sqsEvent.Records {
		log.Printf("Processing SQS message: %s", record.MessageId)

		// Parse work item
		var workItem pkg.WorkItem
		if err := json.Unmarshal([]byte(record.Body), &workItem); err != nil {
			log.Printf("Failed to parse work item: %v", err)
			continue
		}

		// Process based on item type
		if workItem.ItemType == "ec2" {
			err := processEC2Instance(ctx, brClient, dynamoClient, embedModel, genID, workItem)
			if err != nil {
				log.Printf("Failed to process EC2 instance: %v", err)
			}
		} else {
			log.Printf("Unknown item type: %s", workItem.ItemType)
		}
	}

	return nil
}

func processEC2Instance(
	ctx context.Context,
	brClient *bedrockruntime.Client,
	dynamoClient *dynamodb.Client,
	embedModel, genID string,
	workItem pkg.WorkItem,
) error {
	instance := workItem.Instance
	log.Printf("Processing EC2 instance: %s", instance.InstanceID)

	// Marshal instance to JSON for embedding and analysis
	data, err := json.Marshal(instance)
	if err != nil {
		updateError := pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, false, pkg.ReportItem{})
		if updateError != nil {
			log.Printf("Failed to update job progress: %v", updateError)
		}
		return fmt.Errorf("failed to marshal instance %s: %v", instance.InstanceID, err)
	}
	record := string(data)

	// Embedding phase
	emb, err := pkg.EmbedText(ctx, brClient, embedModel, record)
	if err != nil {
		updateError := pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, false, pkg.ReportItem{})
		if updateError != nil {
			log.Printf("Failed to update job progress: %v", updateError)
		}
		return fmt.Errorf("embed error for %s: %v", instance.InstanceID, err)
	}

	// Analysis phase
	analysis, err := pkg.AnalyzeInstance(ctx, brClient, genID, record, instance.CPUAvg7d)
	if err != nil {
		// Still mark as completed but with error in analysis
		reportItem := pkg.ReportItem{
			Instance:  instance,
			Embedding: emb,
			Analysis:  fmt.Sprintf("ERROR: Failed to analyze instance: %v", err),
		}
		updateError := pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, true, reportItem)
		if updateError != nil {
			log.Printf("Failed to update job progress: %v", updateError)
		}
		return fmt.Errorf("analysis error for %s: %v", instance.InstanceID, err)
	}

	// Success - update job progress with result
	reportItem := pkg.ReportItem{
		Instance:  instance,
		Embedding: emb,
		Analysis:  analysis,
	}
	err = pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, true, reportItem)
	if err != nil {
		log.Printf("Failed to update job progress: %v", err)
	}

	// Check if job is complete
	job, err := pkg.GetJob(ctx, dynamoClient, workItem.JobID)
	if err == nil && job.CompletedItems+job.FailedItems >= job.TotalItems {
		// All items processed - update job status
		status := pkg.JobStatusCompleted
		if job.FailedItems > 0 && job.FailedItems == job.TotalItems {
			status = pkg.JobStatusFailed
		}
		err = pkg.UpdateJobStatus(ctx, dynamoClient, workItem.JobID, status)
		if err != nil {
			log.Printf("Failed to update job status: %v", err)
		}
	}

	return nil
}

func main() {
	lambda.Start(Handler)
}
