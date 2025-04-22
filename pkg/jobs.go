// Original file: pkg/jobs.go with modifications
package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
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
		log.Printf("Standard unmarshal failed: %v. Trying alternative approach...", err)

		// Create a map to store the raw data first
		rawJob := make(map[string]interface{})
		err2 := attributevalue.UnmarshalMap(result.Item, &rawJob)
		if err2 != nil {
			return nil, fmt.Errorf("failed to unmarshal job (both methods): %w", err2)
		}

		// Extract basic fields manually
		job.JobID = getStringValue(rawJob, "job_id")
		job.Status = JobStatus(getStringValue(rawJob, "status"))
		job.CreatedAt = getInt64Value(rawJob, "created_at")
		job.UpdatedAt = getInt64Value(rawJob, "updated_at")
		job.CompletedAt = getInt64Value(rawJob, "completed_at")
		job.TotalItems = getIntValue(rawJob, "total_items")
		job.CompletedItems = getIntValue(rawJob, "completed_items")
		job.FailedItems = getIntValue(rawJob, "failed_items")
		job.ExpirationTime = getInt64Value(rawJob, "expiration_time")

		// Handle resource types array
		if resourceTypes, ok := rawJob["resource_types"].([]interface{}); ok {
			for _, rt := range resourceTypes {
				if rtString, ok := rt.(string); ok {
					job.ResourceTypes = append(job.ResourceTypes, rtString)
				}
			}
		}

		// FIXED: Handle results field, which might contain string-encoded JSON
		if resultsRaw, exists := result.Item["results"]; exists {
			if resultsList, ok := resultsRaw.(*types.AttributeValueMemberL); ok {
				job.Results = make([]ReportItem, 0, len(resultsList.Value))

				for _, itemRaw := range resultsList.Value {
					var reportItem ReportItem

					switch v := itemRaw.(type) {
					case *types.AttributeValueMemberM:
						// If it's a proper DynamoDB map, unmarshal it directly
						if err := attributevalue.UnmarshalMap(v.Value, &reportItem); err == nil {
							job.Results = append(job.Results, reportItem)
						} else {
							log.Printf("Warning: Failed to unmarshal map result: %v", err)
						}

					case *types.AttributeValueMemberS:
						// If it's a string (JSON-encoded), parse it
						var reportItemStr string
						attributevalue.Unmarshal(v, &reportItemStr)

						// First try unmarshaling the string directly to a ReportItem
						if err := json.Unmarshal([]byte(reportItemStr), &reportItem); err == nil {
							job.Results = append(job.Results, reportItem)
						} else {
							log.Printf("Warning: Failed to unmarshal string result: %v", err)

							// If that fails, try to clean up the string (handle escaped quotes)
							cleanJSON := strings.ReplaceAll(reportItemStr, "\\\"", "\"")
							if err := json.Unmarshal([]byte(cleanJSON), &reportItem); err == nil {
								job.Results = append(job.Results, reportItem)
							} else {
								log.Printf("Warning: Failed to unmarshal cleaned string result: %v", err)
							}
						}
					}
				}
			}
		}
	}

	return &job, nil
}

// Helper functions to safely extract values from a map
func getStringValue(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok {
		if strVal, ok := val.(string); ok {
			return strVal
		}
	}
	return ""
}

func getIntValue(m map[string]interface{}, key string) int {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		case string:
			if i, err := strconv.Atoi(v); err == nil {
				return i
			}
		}
	}
	return 0
}

func getInt64Value(m map[string]interface{}, key string) int64 {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case int64:
			return v
		case int:
			return int64(v)
		case float64:
			return int64(v)
		case string:
			if i, err := strconv.ParseInt(v, 10, 64); err == nil {
				return i
			}
		}
	}
	return 0
}

// UpdateJobProgress increments the completed items counter for a job
func UpdateJobProgress(ctx context.Context, dynamoClient *dynamodb.Client, jobID string, success bool, result ReportItem) error {
	now := time.Now().Unix()

	if success {
		// For successful completion with results, use a more explicit update
		updateExpr := "SET updated_at = :updated_at, completed_items = completed_items + :inc"
		exprValues := map[string]types.AttributeValue{
			":updated_at": &types.AttributeValueMemberN{Value: strconv.FormatInt(now, 10)},
			":inc":        &types.AttributeValueMemberN{Value: "1"},
			":empty_list": &types.AttributeValueMemberL{Value: []types.AttributeValue{}},
		}

		// Only add the result if it's not empty
		if !IsEmpty(result) {
			// First, get the current job to see if results already exists
			getResult, err := dynamoClient.GetItem(ctx, &dynamodb.GetItemInput{
				TableName: aws.String(os.Getenv("JOBS_TABLE")),
				Key: map[string]types.AttributeValue{
					"job_id": &types.AttributeValueMemberS{Value: jobID},
				},
				ProjectionExpression: aws.String("results"),
			})

			if err != nil {
				log.Printf("Warning: Failed to check current results: %v", err)
			}

			// Marshal the result to a DynamoDB attribute MAP (not a string like before!)
			resultAV, err := attributevalue.MarshalMap(result)
			if err != nil {
				log.Printf("Warning: Failed to marshal result to DynamoDB: %v", err)

				// If marshaling fails, still update the completed count without adding the result
				_, err := dynamoClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{
					TableName: aws.String(os.Getenv("JOBS_TABLE")),
					Key: map[string]types.AttributeValue{
						"job_id": &types.AttributeValueMemberS{Value: jobID},
					},
					UpdateExpression:          aws.String("SET updated_at = :updated_at, completed_items = completed_items + :inc"),
					ExpressionAttributeValues: exprValues,
				})
				if err != nil {
					return fmt.Errorf("failed to update job progress (count only): %w", err)
				}
				return nil
			}

			// Check if the results field exists
			if _, ok := getResult.Item["results"]; ok {
				// Results field exists, append to it
				updateExpr += ", results = list_append(results, :result)"
			} else {
				// Results field doesn't exist, create it
				updateExpr += ", results = list_append(:empty_list, :result)"
			}

			// Add the result to the list as a MAP, not a string
			exprValues[":result"] = &types.AttributeValueMemberL{
				Value: []types.AttributeValue{
					&types.AttributeValueMemberM{Value: resultAV},
				},
			}
		}

		// Perform the update
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
	} else {
		// For failed items, just increment the counter
		updateExpr := "SET updated_at = :updated_at, failed_items = failed_items + :inc"
		exprValues := map[string]types.AttributeValue{
			":updated_at": &types.AttributeValueMemberN{Value: strconv.FormatInt(now, 10)},
			":inc":        &types.AttributeValueMemberN{Value: "1"},
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
