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
	JobID       string      `json:"job_id"`
	ItemIndex   int         `json:"item_index"`
	ItemType    string      `json:"item_type"`
	Instance    Instance    `json:"instance,omitempty"`
	S3Bucket    S3Bucket    `json:"s3_bucket,omitempty"`
	RDSInstance RDSInstance `json:"rds_instance,omitempty"`
	// Add other resource types here later (EBS, etc.)
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
func QueueWorkItem(ctx context.Context, sqsClient *sqs.Client, jobID string, itemIndex int, itemType string, workItem WorkItem) error {
	// Set the job ID and other metadata
	workItem.JobID = jobID
	workItem.ItemIndex = itemIndex
	workItem.ItemType = itemType

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

// GetJob retrieves a job from DynamoDB with robust string handling
func GetJob(ctx context.Context, dynamoClient *dynamodb.Client, jobID string) (*JobInfo, error) {
	log.Printf("Retrieving job %s from DynamoDB", jobID)

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

	// First extract the basic job information (without the results)
	removeResults := copyDynamoItemWithoutResults(result.Item)

	var job JobInfo
	err = attributevalue.UnmarshalMap(removeResults, &job)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal job basic info: %w", err)
	}

	// Now handle results separately
	if resultsAV, hasResults := result.Item["results"]; hasResults {
		log.Printf("Found results field in job %s, processing separately", jobID)

		switch typedResults := resultsAV.(type) {
		case *types.AttributeValueMemberL:
			job.Results = make([]ReportItem, 0)

			for i, resultItemAV := range typedResults.Value {
				reportItem, err := extractReportItem(resultItemAV, i)
				if err != nil {
					log.Printf("Warning: Failed to extract report item %d: %v", i, err)
					continue
				}

				job.Results = append(job.Results, reportItem)
			}

			log.Printf("Successfully extracted %d report items for job %s", len(job.Results), jobID)

		default:
			log.Printf("Warning: Results field is not a list, found type %T instead", resultsAV)
		}
	} else {
		log.Printf("No results field found for job %s", jobID)
		job.Results = []ReportItem{}
	}

	return &job, nil
}

// extractReportItem extracts a ReportItem from a DynamoDB attribute value
func extractReportItem(av types.AttributeValue, index int) (ReportItem, error) {
	var reportItem ReportItem

	switch typedAV := av.(type) {
	case *types.AttributeValueMemberM:
		// If it's a map, try standard unmarshaling
		if err := attributevalue.UnmarshalMap(typedAV.Value, &reportItem); err != nil {
			return reportItem, err
		}

	case *types.AttributeValueMemberS:
		// If it's a string, try various approaches to parse it
		log.Printf("Found string-formatted report item at index %d", index)

		var stringValue string
		if err := attributevalue.Unmarshal(typedAV, &stringValue); err != nil {
			return reportItem, err
		}

		// Try direct JSON unmarshaling
		if err := json.Unmarshal([]byte(stringValue), &reportItem); err != nil {
			log.Printf("Failed direct unmarshal, trying unescape: %v", err)

			// Try unescaping quotes
			cleanJSON := strings.ReplaceAll(stringValue, "\\\"", "\"")
			if err := json.Unmarshal([]byte(cleanJSON), &reportItem); err != nil {
				log.Printf("Failed cleanup unmarshal, trying parse instance directly: %v", err)

				// Last resort - try to manually extract and parse components
				return parseManually(stringValue)
			}
		}

	default:
		return reportItem, fmt.Errorf("unsupported attribute value type: %T", av)
	}

	return reportItem, nil
}

// // parseManually tries to manually extract data from a string representation
func parseManually(jsonStr string) (ReportItem, error) {
	var reportItem ReportItem

	// Check if it's an EC2 instance or S3 bucket based on field names
	if strings.Contains(jsonStr, "instanceId") {
		// Parse as EC2 instance
		var data struct {
			Instance  map[string]interface{} `json:"instance"`
			Embedding []float64              `json:"embedding"`
			Analysis  string                 `json:"analysis"`
		}

		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			// Try with some cleanup
			cleanJSON := strings.ReplaceAll(jsonStr, "\\\"", "\"")
			cleanJSON = strings.ReplaceAll(cleanJSON, "\"\"", "\"")
			if err := json.Unmarshal([]byte(cleanJSON), &data); err != nil {
				return reportItem, fmt.Errorf("failed to manually parse EC2 data: %w", err)
			}
		}

		// Recreate the instance
		instanceData, err := json.Marshal(data.Instance)
		if err != nil {
			return reportItem, err
		}

		err = json.Unmarshal(instanceData, &reportItem.Instance)
		if err != nil {
			return reportItem, err
		}

		reportItem.Embedding = data.Embedding
		reportItem.Analysis = data.Analysis

	} else if strings.Contains(jsonStr, "bucketName") {
		// Parse as S3 bucket
		var data struct {
			S3Bucket  map[string]interface{} `json:"s3_bucket"`
			Embedding []float64              `json:"embedding"`
			Analysis  string                 `json:"analysis"`
		}

		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			// Try with some cleanup
			cleanJSON := strings.ReplaceAll(jsonStr, "\\\"", "\"")
			cleanJSON = strings.ReplaceAll(cleanJSON, "\"\"", "\"")
			if err := json.Unmarshal([]byte(cleanJSON), &data); err != nil {
				return reportItem, fmt.Errorf("failed to manually parse S3 data: %w", err)
			}
		}

		// Recreate the bucket
		bucketData, err := json.Marshal(data.S3Bucket)
		if err != nil {
			return reportItem, err
		}

		err = json.Unmarshal(bucketData, &reportItem.S3Bucket)
		if err != nil {
			return reportItem, err
		}

		reportItem.Embedding = data.Embedding
		reportItem.Analysis = data.Analysis
	} else {
		return reportItem, fmt.Errorf("unable to determine resource type from string: %s", jsonStr)
	}

	return reportItem, nil
}

// copyDynamoItemWithoutResults creates a copy of a DynamoDB item without the 'results' field
func copyDynamoItemWithoutResults(item map[string]types.AttributeValue) map[string]types.AttributeValue {
	newItem := make(map[string]types.AttributeValue, len(item)-1)
	for k, v := range item {
		if k != "results" {
			newItem[k] = v
		}
	}
	return newItem
}

// Helper functions to safely extract values from a map
// func getStringValue(m map[string]interface{}, key string) string {
// 	if val, ok := m[key]; ok {
// 		if strVal, ok := val.(string); ok {
// 			return strVal
// 		}
// 	}
// 	return ""
// }

// func getIntValue(m map[string]interface{}, key string) int {
// 	if val, ok := m[key]; ok {
// 		switch v := val.(type) {
// 		case int:
// 			return v
// 		case int64:
// 			return int(v)
// 		case float64:
// 			return int(v)
// 		case string:
// 			if i, err := strconv.Atoi(v); err == nil {
// 				return i
// 			}
// 		}
// 	}
// 	return 0
// }

// func getInt64Value(m map[string]interface{}, key string) int64 {
// 	if val, ok := m[key]; ok {
// 		switch v := val.(type) {
// 		case int64:
// 			return v
// 		case int:
// 			return int64(v)
// 		case float64:
// 			return int64(v)
// 		case string:
// 			if i, err := strconv.ParseInt(v, 10, 64); err == nil {
// 				return i
// 			}
// 		}
// 	}
// 	return 0
// }

// UpdateJobProgress increments the completed items counter for a job
func UpdateJobProgress(ctx context.Context, dynamoClient *dynamodb.Client, jobID string, success bool, result ReportItem) error {
	now := time.Now().Unix()

	if success {
		// Base update expression and values (only increment and timestamp)
		updateExpr := "SET updated_at = :updated_at, completed_items = completed_items + :inc"
		exprValues := map[string]types.AttributeValue{
			":updated_at": &types.AttributeValueMemberN{Value: strconv.FormatInt(now, 10)},
			":inc":        &types.AttributeValueMemberN{Value: "1"},
		}

		// Only append a ReportItem if it's non-empty
		if !IsEmptyObject(result) {
			// Check whether the "results" list already exists
			getResult, err := dynamoClient.GetItem(ctx, &dynamodb.GetItemInput{
				TableName:            aws.String(os.Getenv("JOBS_TABLE")),
				Key:                  map[string]types.AttributeValue{"job_id": &types.AttributeValueMemberS{Value: jobID}},
				ProjectionExpression: aws.String("results"),
			})
			if err != nil {
				log.Printf("Warning: Failed to check current results: %v", err)
			}

			// Marshal the new ReportItem
			resultAV, err := attributevalue.MarshalMap(result)
			if err != nil {
				log.Printf("Warning: Failed to marshal result: %v", err)
				// Fallback: update count only
				_, err := dynamoClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{
					TableName:                 aws.String(os.Getenv("JOBS_TABLE")),
					Key:                       map[string]types.AttributeValue{"job_id": &types.AttributeValueMemberS{Value: jobID}},
					UpdateExpression:          aws.String(updateExpr),
					ExpressionAttributeValues: exprValues,
				})
				if err != nil {
					return fmt.Errorf("failed to update job progress (count only): %w", err)
				}
				return nil
			}

			// Determine whether to prepend an empty list or append to existing
			if _, exists := getResult.Item["results"]; exists {
				updateExpr += ", results = list_append(results, :result)"
			} else {
				updateExpr += ", results = list_append(:empty_list, :result)"
				exprValues[":empty_list"] = &types.AttributeValueMemberL{Value: []types.AttributeValue{}}
			}

			// Add the new item
			exprValues[":result"] = &types.AttributeValueMemberL{
				Value: []types.AttributeValue{
					&types.AttributeValueMemberM{Value: resultAV},
				},
			}
		}

		// Perform the update
		_, err := dynamoClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:                 aws.String(os.Getenv("JOBS_TABLE")),
			Key:                       map[string]types.AttributeValue{"job_id": &types.AttributeValueMemberS{Value: jobID}},
			UpdateExpression:          aws.String(updateExpr),
			ExpressionAttributeValues: exprValues,
		})
		if err != nil {
			return fmt.Errorf("failed to update job progress: %w", err)
		}

	} else {
		// For failed items, just increment the failed counter
		updateExpr := "SET updated_at = :updated_at, failed_items = failed_items + :inc"
		exprValues := map[string]types.AttributeValue{
			":updated_at": &types.AttributeValueMemberN{Value: strconv.FormatInt(now, 10)},
			":inc":        &types.AttributeValueMemberN{Value: "1"},
		}

		_, err := dynamoClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:                 aws.String(os.Getenv("JOBS_TABLE")),
			Key:                       map[string]types.AttributeValue{"job_id": &types.AttributeValueMemberS{Value: jobID}},
			UpdateExpression:          aws.String(updateExpr),
			ExpressionAttributeValues: exprValues,
		})
		if err != nil {
			return fmt.Errorf("failed to update job progress: %w", err)
		}
	}

	return nil
}

// IsEmptyObject checks if a struct is empty
func IsEmptyObject(obj interface{}) bool {
	// Simple check - this would need to be more robust in production
	jsonData, err := json.Marshal(obj)
	if err != nil {
		return true
	}
	return string(jsonData) == "{}" || string(jsonData) == "null"
}
