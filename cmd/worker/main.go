package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	pkg "github.com/alexalbu001/greenops/pkg"
)

func Handler(ctx context.Context, sqsEvent events.SQSEvent) error {
	log.Printf("DEBUG: SQS Handler invoked—this is the *right* code!")
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
		log.Printf("Raw SQS record body: %s", record.Body) //DEBUG

		// Parse work item
		var workItem pkg.WorkItem
		if err := json.Unmarshal([]byte(record.Body), &workItem); err != nil {
			log.Printf("Failed to parse work item: %v", err)
			continue
		}
		log.Printf("Parsed workItem.ItemType = %q", workItem.ItemType)

		// Dispatch based on item type
		switch workItem.ItemType {
		case "ec2":
			if err := processEC2Instance(ctx, brClient, dynamoClient, embedModel, genID, workItem); err != nil {
				log.Printf("Failed to process EC2 instance: %v", err)
			}
		case "s3":
			if err := processS3Bucket(ctx, brClient, dynamoClient, embedModel, genID, workItem); err != nil {
				log.Printf("Failed to process S3 bucket: %v", err)
			}
		case "rds":
			if err := processRDSInstance(ctx, brClient, dynamoClient, embedModel, genID, workItem); err != nil {
				log.Printf("Failed to process RDS instance: %v", err)
			}
		default:
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

	// Marshal instance to JSON
	data, err := json.Marshal(instance)
	if err != nil {
		pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, false, pkg.ReportItem{})
		return fmt.Errorf("failed to marshal instance %s: %v", instance.InstanceID, err)
	}
	record := string(data)

	// Embedding phase
	emb, err := pkg.EmbedText(ctx, brClient, embedModel, record)
	if err != nil {
		pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, false, pkg.ReportItem{})
		return fmt.Errorf("embed error for %s: %v", instance.InstanceID, err)
	}

	// Analysis phase: Bedrock first
	analysis, err := pkg.AnalyzeInstance(ctx, brClient, genID, record, instance.CPUAvg7d)
	if err != nil || analysis == "" {
		log.Printf("Bedrock analysis failed for EC2 %s: %v", instance.InstanceID, err)
		// No local fallback for EC2—report error message
		analysis = fmt.Sprintf("ERROR: Failed to analyze instance: %v", err)
	}

	// Update progress
	reportItem := pkg.ReportItem{
		ResourceType: pkg.ResourceTypeEC2,
		Instance:     instance,
		Embedding:    emb,
		Analysis:     analysis,
	}
	pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, true, reportItem)

	// Finalize job status if needed
	job, err := pkg.GetJob(ctx, dynamoClient, workItem.JobID)
	if err == nil && (job.CompletedItems+job.FailedItems >= job.TotalItems) &&
		(job.Status != pkg.JobStatusCompleted && job.Status != pkg.JobStatusFailed) {
		status := pkg.JobStatusCompleted
		if job.FailedItems == job.TotalItems {
			status = pkg.JobStatusFailed
		}
		pkg.UpdateJobStatus(ctx, dynamoClient, workItem.JobID, status)
	}

	return nil
}

func processS3Bucket(
	ctx context.Context,
	brClient *bedrockruntime.Client,
	dynamoClient *dynamodb.Client,
	embedModel, genID string,
	workItem pkg.WorkItem,
) error {
	bucket := workItem.S3Bucket
	log.Printf("Processing S3 bucket: %s (region: %s)", bucket.BucketName, bucket.Region)

	// Timeout context
	processingCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Marshal bucket
	data, err := json.Marshal(bucket)
	if err != nil {
		pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, false, pkg.ReportItem{})
		return fmt.Errorf("failed to marshal bucket %s: %v", bucket.BucketName, err)
	}
	record := string(data)

	// Embedding
	emb, err := pkg.EmbedText(processingCtx, brClient, embedModel, record)
	if err != nil {
		pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, false, pkg.ReportItem{})
		return fmt.Errorf("embed error for bucket %s: %v", bucket.BucketName, err)
	}

	// Analysis phase: Bedrock first (45s timeout)
	analysisCtx, cancelAnalysis := context.WithTimeout(processingCtx, 45*time.Second)
	defer cancelAnalysis()

	analysis, err := pkg.AnalyzeS3BucketWithBedrock(analysisCtx, brClient, genID, bucket, emb)
	if err != nil || analysis == "" {
		log.Printf("Bedrock analysis failed for S3 %s: %v", bucket.BucketName, err)
	}

	// Update progress
	reportItem := pkg.ReportItem{
		ResourceType: pkg.ResourceTypeS3,
		S3Bucket:     bucket,
		Embedding:    emb,
		Analysis:     analysis,
	}
	pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, true, reportItem)

	// Finalize job status
	job, err := pkg.GetJob(ctx, dynamoClient, workItem.JobID)
	if err == nil && (job.CompletedItems+job.FailedItems >= job.TotalItems) &&
		(job.Status != pkg.JobStatusCompleted && job.Status != pkg.JobStatusFailed) {
		status := pkg.JobStatusCompleted
		if job.FailedItems == job.TotalItems {
			status = pkg.JobStatusFailed
		}
		pkg.UpdateJobStatus(ctx, dynamoClient, workItem.JobID, status)
	}

	return nil
}

func processRDSInstance(
	ctx context.Context,
	brClient *bedrockruntime.Client,
	dynamoClient *dynamodb.Client,
	embedModel, genID string,
	workItem pkg.WorkItem,
) error {
	instance := workItem.RDSInstance
	log.Printf("Processing RDS instance: %s", instance.InstanceID)

	processingCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Marshal instance
	data, err := json.Marshal(instance)
	if err != nil {
		pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, false, pkg.ReportItem{})
		return fmt.Errorf("failed to marshal RDS instance %s: %v", instance.InstanceID, err)
	}
	record := string(data)

	// Embedding
	emb, err := pkg.EmbedText(processingCtx, brClient, embedModel, record)
	if err != nil {
		pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, false, pkg.ReportItem{})
		return fmt.Errorf("embed error for RDS %s: %v", instance.InstanceID, err)
	}

	// Analysis phase: Bedrock first (45s timeout)
	analysisCtx, cancelAnalysis := context.WithTimeout(processingCtx, 45*time.Second)
	defer cancelAnalysis()

	analysis, err := pkg.AnalyzeRDSInstanceWithBedrock(analysisCtx, brClient, genID, instance, emb)
	if err != nil || analysis == "" {
		log.Printf("Bedrock analysis failed for RDS %s: %v", instance.InstanceID, err)
		// Fallback to local
		if local, locErr := pkg.AnalyzeRDSInstance(ctx, instance); locErr == nil {
			analysis = local.Analysis
		} else {
			analysis = fmt.Sprintf("ERROR: Failed to analyze RDS instance: %v", err)
		}
	}

	// Update progress
	reportItem := pkg.ReportItem{
		ResourceType: pkg.ResourceTypeRDS,
		RDSInstance:  instance,
		Embedding:    emb,
		Analysis:     analysis,
	}
	pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, true, reportItem)

	// Finalize job status
	job, err := pkg.GetJob(ctx, dynamoClient, workItem.JobID)
	if err == nil && (job.CompletedItems+job.FailedItems >= job.TotalItems) &&
		(job.Status != pkg.JobStatusCompleted && job.Status != pkg.JobStatusFailed) {
		status := pkg.JobStatusCompleted
		if job.FailedItems == job.TotalItems {
			status = pkg.JobStatusFailed
		}
		pkg.UpdateJobStatus(ctx, dynamoClient, workItem.JobID, status)
	}

	return nil
}

func main() {
	lambda.Start(Handler)
}
