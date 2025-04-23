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
		//DEBUG
		log.Printf("Parsed workItem.ItemType = %q", workItem.ItemType)

		// Process based on item type
		switch workItem.ItemType {
		case "ec2":
			err := processEC2Instance(ctx, brClient, dynamoClient, embedModel, genID, workItem)
			if err != nil {
				log.Printf("Failed to process EC2 instance: %v", err)
			}
		case "s3":
			err := processS3Bucket(ctx, brClient, dynamoClient, embedModel, genID, workItem)
			if err != nil {
				log.Printf("Failed to process S3 bucket: %v", err)
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
			ResourceType: pkg.ResourceTypeEC2, // Explicitly set the resource type
			Instance:     instance,
			Embedding:    emb,
			Analysis:     fmt.Sprintf("ERROR: Failed to analyze instance: %v", err),
		}
		updateError := pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, true, reportItem)
		if updateError != nil {
			log.Printf("Failed to update job progress: %v", updateError)
		}
		return fmt.Errorf("analysis error for %s: %v", instance.InstanceID, err)
	}

	// Success - update job progress with result
	reportItem := pkg.ReportItem{
		ResourceType: pkg.ResourceTypeEC2, // Explicitly set the resource type
		Instance:     instance,
		Embedding:    emb,
		Analysis:     analysis,
	}
	err = pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, true, reportItem)
	if err != nil {
		log.Printf("Failed to update job progress: %v", err)
	}

	// Check if job is complete
	job, err := pkg.GetJob(ctx, dynamoClient, workItem.JobID)
	if err == nil {
		log.Printf("Job status check: JobID=%s, Completed=%d, Failed=%d, Total=%d",
			job.JobID, job.CompletedItems, job.FailedItems, job.TotalItems)

		// If all items are processed (completed + failed >= total)
		// And the job isn't already in a terminal state
		if (job.CompletedItems+job.FailedItems >= job.TotalItems) &&
			(job.Status != pkg.JobStatusCompleted && job.Status != pkg.JobStatusFailed) {

			// Determine final status
			status := pkg.JobStatusCompleted
			if job.FailedItems > 0 && job.FailedItems == job.TotalItems {
				status = pkg.JobStatusFailed
			}

			log.Printf("All items processed. Updating job %s status to %s", job.JobID, status)
			err = pkg.UpdateJobStatus(ctx, dynamoClient, workItem.JobID, status)
			if err != nil {
				log.Printf("Failed to update job status: %v", err)
			} else {
				log.Printf("Successfully updated job status to %s", status)
			}
		}
	} else {
		log.Printf("Failed to get job for status update: %v", err)
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
	log.Printf("Processing S3 bucket: %s in region %s", bucket.BucketName, bucket.Region)

	// Create a context with timeout to prevent hanging
	processingCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// Marshal bucket to JSON for embedding
	data, err := json.Marshal(bucket)
	if err != nil {
		log.Printf("Failed to marshal bucket %s: %v", bucket.BucketName, err)
		// Mark as failed but continue
		updateError := pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, false, pkg.ReportItem{})
		if updateError != nil {
			log.Printf("Failed to update job progress: %v", updateError)
		}
		return fmt.Errorf("failed to marshal bucket %s: %v", bucket.BucketName, err)
	}
	record := string(data)

	// Embedding phase - with timeout handling
	var emb []float64
	embedDone := make(chan bool, 1)

	go func() {
		var embedErr error
		emb, embedErr = pkg.EmbedText(processingCtx, brClient, embedModel, record)
		if embedErr != nil {
			log.Printf("Embed error for %s: %v", bucket.BucketName, embedErr)
		}
		embedDone <- (embedErr == nil)
	}()

	// Wait with timeout
	select {
	case success := <-embedDone:
		if !success {
			// Mark as failed but continue
			updateError := pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, false, pkg.ReportItem{})
			if updateError != nil {
				log.Printf("Failed to update job progress: %v", updateError)
			}
			return fmt.Errorf("embedding failed for %s", bucket.BucketName)
		}
	case <-time.After(30 * time.Second):
		// Timeout
		log.Printf("Embedding timed out for bucket %s", bucket.BucketName)
		updateError := pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, false, pkg.ReportItem{})
		if updateError != nil {
			log.Printf("Failed to update job progress: %v", updateError)
		}
		return fmt.Errorf("embedding timed out for %s", bucket.BucketName)
	}

	// Analysis phase - try local analysis first, then Bedrock if available
	var analysis string
	var bucketAnalysis pkg.S3BucketAnalysis

	// Try local analysis first (more reliable)
	bucketAnalysis, err = pkg.AnalyzeS3Bucket(ctx, bucket)
	if err != nil {
		log.Printf("Local analysis failed for %s: %v", bucket.BucketName, err)
		// Try Bedrock anyway
	} else {
		// Use the local analysis result
		analysis = bucketAnalysis.Analysis
	}

	// If local analysis failed or is empty, try Bedrock
	if analysis == "" {
		// Analysis with Bedrock - with timeout handling
		analysisDone := make(chan bool, 1)

		go func() {
			var analysisErr error
			analysis, analysisErr = pkg.AnalyzeS3BucketWithBedrock(processingCtx, brClient, genID, bucket, emb)
			if analysisErr != nil {
				log.Printf("Bedrock analysis error for %s: %v", bucket.BucketName, analysisErr)
			}
			analysisDone <- (analysisErr == nil)
		}()

		// Wait with timeout
		select {
		case success := <-analysisDone:
			if !success && bucketAnalysis.Analysis != "" {
				// Use local analysis as fallback
				analysis = bucketAnalysis.Analysis
			} else if !success {
				// If both failed, use a generic analysis
				analysis = fmt.Sprintf("Failed to analyze bucket %s. This bucket may need manual review.", bucket.BucketName)
			}
		case <-time.After(45 * time.Second):
			// Timeout - use local analysis as fallback or generic message
			log.Printf("Analysis timed out for bucket %s", bucket.BucketName)
			if bucketAnalysis.Analysis != "" {
				analysis = bucketAnalysis.Analysis
			} else {
				analysis = fmt.Sprintf("Analysis timed out for bucket %s. This bucket may need manual review.", bucket.BucketName)
			}
		}
	}

	// Success - update job progress with result, even if analysis had issues
	reportItem := pkg.ReportItem{
		ResourceType: pkg.ResourceTypeS3, // IMPORTANT: Explicitly set the resource type
		S3Bucket:     bucket,
		Embedding:    emb,
		Analysis:     analysis,
	}

	err = pkg.UpdateJobProgress(ctx, dynamoClient, workItem.JobID, true, reportItem)
	if err != nil {
		log.Printf("Failed to update job progress: %v", err)
	} else {
		log.Printf("Successfully processed S3 bucket: %s", bucket.BucketName)
	}

	// Check if job is complete
	job, err := pkg.GetJob(ctx, dynamoClient, workItem.JobID)
	if err == nil {
		log.Printf("Job status check: JobID=%s, Completed=%d, Failed=%d, Total=%d",
			job.JobID, job.CompletedItems, job.FailedItems, job.TotalItems)

		// If all items are processed (completed + failed >= total)
		// And the job isn't already in a terminal state
		if (job.CompletedItems+job.FailedItems >= job.TotalItems) &&
			(job.Status != pkg.JobStatusCompleted && job.Status != pkg.JobStatusFailed) {

			// Determine final status
			status := pkg.JobStatusCompleted
			if job.FailedItems > 0 && job.FailedItems == job.TotalItems {
				status = pkg.JobStatusFailed
			}

			log.Printf("All items processed. Updating job %s status to %s", job.JobID, status)
			err = pkg.UpdateJobStatus(ctx, dynamoClient, workItem.JobID, status)
			if err != nil {
				log.Printf("Failed to update job status: %v", err)
			} else {
				log.Printf("Successfully updated job status to %s", status)
			}
		}
	} else {
		log.Printf("Failed to get job for status update: %v", err)
	}

	return nil
}

func main() {
	lambda.Start(Handler)
}
