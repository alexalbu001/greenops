package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	pkg "github.com/alexalbu001/greenops/pkg"
)

// ServerRequest represents incoming payload of resources to analyze
type ServerRequest struct {
	Instances    []pkg.Instance    `json:"instances"`
	S3Buckets    []pkg.S3Bucket    `json:"s3_buckets"`
	RDSInstances []pkg.RDSInstance `json:"rds_instances"`
}

// Handler is the Lambda entrypoint
func Handler(ctx context.Context, apiReq events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	log.Printf("Received event: %s", apiReq.RawPath)

	// Check if this is a job status request
	if apiReq.RouteKey == "GET /jobs/{id}" {
		return HandleJobStatus(ctx, apiReq)
	}

	// Check if this is a job results request
	if apiReq.RouteKey == "GET /jobs/{id}/results" {
		return HandleJobResults(ctx, apiReq)
	}

	// Original analyze request
	log.Printf("Received analyze request: %s", apiReq.Body)

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
	totalResources := len(req.Instances) + len(req.S3Buckets) + len(req.RDSInstances)
	if totalResources == 0 {
		log.Printf("request contained no resources to analyze")
		return events.APIGatewayV2HTTPResponse{
			StatusCode: 400,
			Body:       `{"error":"no resources provided in request"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	// Load AWS config for Bedrock, DynamoDB, and SQS
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Printf("unable to load AWS config: %v", err)
		return events.APIGatewayV2HTTPResponse{
			StatusCode: 500,
			Body:       `{"error":"failed to initialize AWS client"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	// Create AWS service clients
	dynamoClient := dynamodb.NewFromConfig(cfg)
	sqsClient := sqs.NewFromConfig(cfg)

	// Create job record with resource types
	resourceTypes := []string{}
	if len(req.Instances) > 0 {
		resourceTypes = append(resourceTypes, "ec2")
	}
	if len(req.S3Buckets) > 0 {
		resourceTypes = append(resourceTypes, "s3")
	}
	if len(req.RDSInstances) > 0 {
		resourceTypes = append(resourceTypes, "rds")
	}

	jobID, err := pkg.CreateJob(ctx, dynamoClient, resourceTypes, totalResources)
	if err != nil {
		log.Printf("failed to create job: %v", err)
		return events.APIGatewayV2HTTPResponse{
			StatusCode: 500,
			Body:       fmt.Sprintf(`{"error":"failed to create job: %v"}`, err),
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	// Queue instances for processing
	itemIndex := 0

	// Queue EC2 instances
	for i, instance := range req.Instances {
		workItem := pkg.WorkItem{
			JobID:     jobID,
			ItemIndex: itemIndex + i,
			ItemType:  "ec2",
			Instance:  instance,
		}

		err := pkg.QueueWorkItem(ctx, sqsClient, jobID, itemIndex+i, "ec2", workItem)
		if err != nil {
			log.Printf("failed to queue instance %s: %v", instance.InstanceID, err)
			// Continue with other resources even if one fails
		}
	}
	itemIndex += len(req.Instances)

	// Queue S3 buckets
	for i, bucket := range req.S3Buckets {
		workItem := pkg.WorkItem{
			JobID:     jobID,
			ItemIndex: itemIndex + i,
			ItemType:  "s3",
			S3Bucket:  bucket,
		}

		err := pkg.QueueWorkItem(ctx, sqsClient, jobID, itemIndex+i, "s3", workItem)
		if err != nil {
			log.Printf("failed to queue bucket %s: %v", bucket.BucketName, err)
			// Continue with other resources even if one fails
		}
	}
	itemIndex += len(req.S3Buckets)

	for i, rdsInstance := range req.RDSInstances {
		workItem := pkg.WorkItem{
			JobID:       jobID,
			ItemIndex:   itemIndex + i,
			ItemType:    "rds",
			RDSInstance: rdsInstance,
		}

		err := pkg.QueueWorkItem(ctx, sqsClient, jobID, itemIndex+i, "rds", workItem)
		if err != nil {
			log.Printf("failed to queue RDS instance %s: %v", rdsInstance.InstanceID, err)
			// Continue with other resources even if one fails
		}
	}
	itemIndex += len(req.RDSInstances)

	// Update job status to processing
	err = pkg.UpdateJobStatus(ctx, dynamoClient, jobID, pkg.JobStatusProcessing)
	if err != nil {
		log.Printf("failed to update job status: %v", err)
		// Continue anyway, not critical
	}

	// Return job ID to client
	return events.APIGatewayV2HTTPResponse{
		StatusCode: 202, // Accepted
		Body:       fmt.Sprintf(`{"job_id":"%s","status":"processing","total_items":%d}`, jobID, totalResources),
		Headers:    map[string]string{"Content-Type": "application/json"},
	}, nil
}

// HandleJobStatus handles GET /jobs/{id} requests
func HandleJobStatus(ctx context.Context, apiReq events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	jobID := apiReq.PathParameters["id"]
	if jobID == "" {
		return events.APIGatewayV2HTTPResponse{
			StatusCode: 400,
			Body:       `{"error":"missing job ID"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	// Check for force_complete parameter
	forceComplete := false
	if apiReq.QueryStringParameters != nil {
		_, forceComplete = apiReq.QueryStringParameters["force_complete"]
	}

	// Load AWS config
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return events.APIGatewayV2HTTPResponse{
			StatusCode: 500,
			Body:       fmt.Sprintf(`{"error":"failed to initialize AWS client: %v"}`, err),
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	// Create DynamoDB client
	dynamoClient := dynamodb.NewFromConfig(cfg)

	// Get job info
	job, err := pkg.GetJob(ctx, dynamoClient, jobID)
	if err != nil {
		if err.Error() == "job not found" {
			return events.APIGatewayV2HTTPResponse{
				StatusCode: 404,
				Body:       `{"error":"job not found"}`,
				Headers:    map[string]string{"Content-Type": "application/json"},
			}, nil
		}

		return events.APIGatewayV2HTTPResponse{
			StatusCode: 500,
			Body:       fmt.Sprintf(`{"error":"failed to get job: %v"}`, err),
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	// If forceComplete=true and all items are processed, update status to completed
	if forceComplete &&
		job.Status == pkg.JobStatusProcessing &&
		(job.CompletedItems+job.FailedItems >= job.TotalItems) {

		newStatus := pkg.JobStatusCompleted
		if job.FailedItems > 0 && job.FailedItems == job.TotalItems {
			newStatus = pkg.JobStatusFailed
		}

		log.Printf("Forcing job %s status from %s to %s", jobID, job.Status, newStatus)
		err = pkg.UpdateJobStatus(ctx, dynamoClient, jobID, newStatus)
		if err != nil {
			log.Printf("Warning: Failed to force update job status: %v", err)
		} else {
			// Update local job object to reflect new status
			job.Status = newStatus
		}
	}

	// Job is in a terminal state (completed or failed), return full result
	if job.Status == pkg.JobStatusCompleted || job.Status == pkg.JobStatusFailed {
		resultsJSON, err := json.Marshal(job.Results)
		if err != nil {
			return events.APIGatewayV2HTTPResponse{
				StatusCode: 500,
				Body:       fmt.Sprintf(`{"error":"failed to marshal results: %v"}`, err),
				Headers:    map[string]string{"Content-Type": "application/json"},
			}, nil
		}

		return events.APIGatewayV2HTTPResponse{
			StatusCode: 200,
			Body: fmt.Sprintf(
				`{"job_id":"%s","status":"%s","total_items":%d,"completed_items":%d,"failed_items":%d,"results":%s}`,
				job.JobID, job.Status, job.TotalItems, job.CompletedItems, job.FailedItems, string(resultsJSON),
			),
			Headers: map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	// Special case: if all items are processed but status is still "processing"
	// Return HTTP 200 instead of 202 and include the results
	if job.Status == pkg.JobStatusProcessing && (job.CompletedItems+job.FailedItems >= job.TotalItems) {
		log.Printf("All items for job %s are processed but status is still %s. Returning results anyway.",
			job.JobID, job.Status)

		resultsJSON, err := json.Marshal(job.Results)
		if err != nil {
			return events.APIGatewayV2HTTPResponse{
				StatusCode: 500,
				Body:       fmt.Sprintf(`{"error":"failed to marshal results: %v"}`, err),
				Headers:    map[string]string{"Content-Type": "application/json"},
			}, nil
		}

		return events.APIGatewayV2HTTPResponse{
			StatusCode: 200, // Return OK instead of Accepted in this case
			Body: fmt.Sprintf(
				`{"job_id":"%s","status":"%s","total_items":%d,"completed_items":%d,"failed_items":%d,"results":%s}`,
				job.JobID, job.Status, job.TotalItems, job.CompletedItems, job.FailedItems, string(resultsJSON),
			),
			Headers: map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	// Job is still processing, return progress
	return events.APIGatewayV2HTTPResponse{
		StatusCode: 202, // Accepted
		Body: fmt.Sprintf(
			`{"job_id":"%s","status":"%s","total_items":%d,"completed_items":%d,"failed_items":%d}`,
			job.JobID, job.Status, job.TotalItems, job.CompletedItems, job.FailedItems,
		),
		Headers: map[string]string{"Content-Type": "application/json"},
	}, nil
}

// New function to handle direct results access
func HandleJobResults(ctx context.Context, apiReq events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	jobID := apiReq.PathParameters["id"]
	if jobID == "" {
		return events.APIGatewayV2HTTPResponse{
			StatusCode: 400,
			Body:       `{"error":"missing job ID"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	// Load AWS config
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return events.APIGatewayV2HTTPResponse{
			StatusCode: 500,
			Body:       fmt.Sprintf(`{"error":"failed to initialize AWS client: %v"}`, err),
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	// Create DynamoDB client
	dynamoClient := dynamodb.NewFromConfig(cfg)

	// Get job directly from DynamoDB
	log.Printf("Getting results for job %s", jobID)
	job, err := pkg.GetJob(ctx, dynamoClient, jobID)
	if err != nil {
		if err.Error() == "job not found" {
			return events.APIGatewayV2HTTPResponse{
				StatusCode: 404,
				Body:       `{"error":"job not found"}`,
				Headers:    map[string]string{"Content-Type": "application/json"},
			}, nil
		}

		return events.APIGatewayV2HTTPResponse{
			StatusCode: 500,
			Body:       fmt.Sprintf(`{"error":"failed to get job: %v"}`, err),
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	// Return just the results array, even if job is not completed
	resultsJSON, err := json.Marshal(job.Results)
	if err != nil {
		return events.APIGatewayV2HTTPResponse{
			StatusCode: 500,
			Body:       fmt.Sprintf(`{"error":"failed to marshal results: %v"}`, err),
			Headers:    map[string]string{"Content-Type": "application/json"},
		}, nil
	}

	// Log the number of results for debugging
	log.Printf("Returning %d results for job %s", len(job.Results), jobID)

	return events.APIGatewayV2HTTPResponse{
		StatusCode: 200,
		Body:       fmt.Sprintf(`{"results":%s}`, string(resultsJSON)),
		Headers:    map[string]string{"Content-Type": "application/json"},
	}, nil
}

func main() {
	lambda.Start(Handler)
}
