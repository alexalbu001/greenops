package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
)

// JobStatus represents the current state of a job
type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusProcessing JobStatus = "processing"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
)

// JobInfo represents a job record in DynamoDB
type JobInfo struct {
	JobID          string       `json:"job_id" dynamodbav:"job_id"`
	Status         JobStatus    `json:"status" dynamodbav:"status"`
	CreatedAt      int64        `json:"created_at" dynamodbav:"created_at"`
	UpdatedAt      int64        `json:"updated_at" dynamodbav:"updated_at"`
	CompletedAt    int64        `json:"completed_at,omitempty" dynamodbav:"completed_at,omitempty"`
	TotalItems     int          `json:"total_items" dynamodbav:"total_items"`
	CompletedItems int          `json:"completed_items" dynamodbav:"completed_items"`
	FailedItems    int          `json:"failed_items" dynamodbav:"failed_items"`
	Results        []ReportItem `json:"results,omitempty" dynamodbav:"results,omitempty"`
	ResourceTypes  []string     `json:"resource_types" dynamodbav:"resource_types"`
	ExpirationTime int64        `json:"expiration_time" dynamodbav:"expiration_time"`
}

// WorkItem represents a single task to be processed
type WorkItem struct {
	JobID     string   `json:"job_id"`
	ItemIndex int      `json:"item_index"`
	ItemType  string   `json:"item_type"`
	Instance  Instance `json:"instance,omitempty"`
	// Add other resource types here later (S3, RDS, etc.)
}

// CreateJob creates a new job record in DynamoDB
func CreateJob(ctx context.Context, dynamoClient *dynamodb.Client, resourceTypes []string, itemCount int) (string, error) {
	jobID := uuid.New().String()
	now := time.Now().Unix()

	// TTL of 7 days
	expirationTime := now + (7 * 24 * 60 * 60)

	job := JobInfo{
		JobID:          jobID,
		Status:         JobStatusPending,
		CreatedAt:      now,
		UpdatedAt:      now,
		TotalItems:     itemCount,
		CompletedItems: 0,
		FailedItems:    0,
		ResourceTypes:  resourceTypes,
		ExpirationTime: expirationTime,
		Results:        make([]ReportItem, 0),
	}

	item, err := attributevalue.MarshalMap(job)
	if err != nil {
		return "", fmt.Errorf("failed to marshal job: %w", err)
	}

	_, err = dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(os.Getenv("JOBS_TABLE")),
		Item:      item,
	})

	if err != nil {
		return "", fmt.Errorf("failed to save job: %w", err)
	}

	log.Printf("Created job %s with %d items", jobID, itemCount)
	return jobID, nil
}

// QueueWorkItem adds a work item to the SQS queue
func QueueWorkItem(ctx context.Context, sqsClient *sqs.Client, jobID string, itemIndex int, itemType string, instance Instance) error {
	workItem := WorkItem{
		JobID:     jobID,
		ItemIndex: itemIndex,
		ItemType:  itemType,
		Instance:  instance,
	}

	body, err := json.Marshal(workItem)
	if err != nil {
		return fmt.Errorf("failed to marshal work item: %w", err)
	}

	_, err = sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(os.Getenv("QUEUE_URL")),
		MessageBody: aws.String(string(body)),
	})

	if err != nil {
		return fmt.Errorf("failed to queue work item: %w", err)
	}

	return nil
}

// UpdateJobStatus updates the status of a job in DynamoDB
func UpdateJobStatus(ctx context.Context, dynamoClient *dynamodb.Client, jobID string, status JobStatus) error {
	now := time.Now().Unix()

	update := map[string]types.AttributeValue{
		":status":     &types.AttributeValueMemberS{Value: string(status)},
		":updated_at": &types.AttributeValueMemberN{Value: strconv.FormatInt(now, 10)},
	}

	updateExp := "SET #status = :status, updated_at = :updated_at"

	// If completing, set completion time
	if status == JobStatusCompleted || status == JobStatusFailed {
		update[":completed_at"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(now, 10)}
		updateExp += ", completed_at = :completed_at"
	}

	_, err := dynamoClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(os.Getenv("JOBS_TABLE")),
		Key: map[string]types.AttributeValue{
			"job_id": &types.AttributeValueMemberS{Value: jobID},
		},
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: update,
		UpdateExpression:          aws.String(updateExp),
	})

	if err != nil {
		return fmt.Errorf("failed to update job status: %w", err)
	}

	return nil
}

// GetJob retrieves a job from DynamoDB
func GetJob(ctx context.Context, dynamoClient *dynamodb.Client, jobID string) (*JobInfo, error) {
	result, err := dynamoClient.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(os.Getenv("JOBS_TABLE")),
		Key: map[string]types.AttributeValue{
			"job_id": &types.AttributeValueMemberS{Value: jobID},
		},
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get job: %w", err)
	}

	if result.Item == nil {
		return nil, fmt.Errorf("job not found")
	}

	var job JobInfo
	err = attributevalue.UnmarshalMap(result.Item, &job)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal job: %w", err)
	}

	return &job, nil
}

// UpdateJobProgress increments the completed items counter for a job
func UpdateJobProgress(ctx context.Context, dynamoClient *dynamodb.Client, jobID string, success bool, result ReportItem) error {
	now := time.Now().Unix()

	updateExpr := "SET updated_at = :updated_at"
	exprValues := map[string]types.AttributeValue{
		":updated_at": &types.AttributeValueMemberN{Value: strconv.FormatInt(now, 10)},
		":inc":        &types.AttributeValueMemberN{Value: "1"},
	}

	if success {
		updateExpr += ", completed_items = completed_items + :inc"

		// Add result to results array if provided
		if !IsEmpty(result) {
			// Marshal the result to a DynamoDB attribute value
			resultAV, err := attributevalue.Marshal(result)
			if err == nil {
				// Append to results list
				updateExpr += ", results = list_append(if_not_exists(results, :empty_list), :result)"
				exprValues[":result"] = &types.AttributeValueMemberL{
					Value: []types.AttributeValue{resultAV},
				}
				exprValues[":empty_list"] = &types.AttributeValueMemberL{Value: []types.AttributeValue{}}
			} else {
				log.Printf("Warning: Failed to marshal result: %v", err)
			}
		}
	} else {
		updateExpr += ", failed_items = failed_items + :inc"
	}

	_, err := dynamoClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(os.Getenv("JOBS_TABLE")),
		Key: map[string]types.AttributeValue{
			"job_id": &types.AttributeValueMemberS{Value: jobID},
		},
		UpdateExpression:          aws.String(updateExpr),
		ExpressionAttributeValues: exprValues,
	})

	if err != nil {
		return fmt.Errorf("failed to update job progress: %w", err)
	}

	return nil
}

// IsEmpty checks if a struct is empty
func IsEmpty(obj interface{}) bool {
	// Simple check - this would need to be more robust in production
	jsonData, err := json.Marshal(obj)
	if err != nil {
		return true
	}
	return string(jsonData) == "{}" || string(jsonData) == "null"
}
