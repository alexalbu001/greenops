package pkg

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Bucket holds metadata and computed metrics for an S3 bucket
type S3Bucket struct {
	BucketName      string              `json:"bucketName"`
	CreationDate    time.Time           `json:"creationDate"`
	Region          string              `json:"region"`
	SizeBytes       int64               `json:"sizeBytes"`
	ObjectCount     int64               `json:"objectCount"`
	StorageClasses  map[string]int64    `json:"storageClasses"`  // Map of storage class to bytes
	AccessFrequency map[string]float64  `json:"accessFrequency"` // GET/PUT/DELETE ops per day
	LifecycleRules  []LifecycleRuleInfo `json:"lifecycleRules"`
	Tags            map[string]string   `json:"tags"`
	LastModified    time.Time           `json:"lastModified"`
}

// LifecycleRuleInfo contains simplified lifecycle rule information
type LifecycleRuleInfo struct {
	ID                 string `json:"id"`
	Status             string `json:"status"` // Enabled/Disabled
	HasTransitions     bool   `json:"hasTransitions"`
	HasExpirations     bool   `json:"hasExpirations"`
	ObjectAgeThreshold int    `json:"objectAgeThreshold"` // Days until first transition/expiration
}

// ListBuckets retrieves all S3 buckets and their key metrics
func ListBuckets(
	ctx context.Context,
	s3Client *s3.Client,
	cwClient *cloudwatch.Client,
	maxBuckets int,
) ([]S3Bucket, error) {
	// Get list of buckets
	bucketList, err := s3Client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, err
	}

	// Apply limit if specified
	buckets := bucketList.Buckets
	if maxBuckets > 0 && len(buckets) > maxBuckets {
		buckets = buckets[:maxBuckets]
	}

	log.Printf("Processing %d S3 buckets (out of %d total)", len(buckets), len(bucketList.Buckets))

	// Process buckets in parallel with a worker pool
	results := make([]S3Bucket, 0, len(buckets))
	resultsMutex := &sync.Mutex{}
	wg := &sync.WaitGroup{}
	semaphore := make(chan struct{}, 5) // Limit to 5 concurrent requests

	for _, bucket := range buckets {
		wg.Add(1)

		go func(b s3Types.Bucket) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Set a timeout for processing each bucket
			bucketCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			// Collect bucket data
			bucketData, err := collectBucketData(bucketCtx, s3Client, cwClient, *b.Name, b.CreationDate)
			if err != nil {
				log.Printf("Warning: Error collecting data for bucket %s: %v", *b.Name, err)
				return
			}

			// Add to results
			resultsMutex.Lock()
			results = append(results, bucketData)
			resultsMutex.Unlock()
		}(bucket)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	return results, nil
}

// collectBucketData gathers all relevant data for a single bucket
func collectBucketData(
	ctx context.Context,
	s3Client *s3.Client,
	cwClient *cloudwatch.Client,
	bucketName string,
	creationDate *time.Time,
) (S3Bucket, error) {
	bucket := S3Bucket{
		BucketName:      bucketName,
		StorageClasses:  make(map[string]int64),
		AccessFrequency: make(map[string]float64),
		Tags:            make(map[string]string),
	}

	if creationDate != nil {
		bucket.CreationDate = *creationDate
	}

	// Get bucket region
	region, err := getBucketRegion(ctx, s3Client, bucketName)
	if err != nil {
		log.Printf("Warning: Unable to determine region for bucket %s: %v", bucketName, err)
	}
	bucket.Region = region

	// Get bucket tags
	tags, err := getBucketTags(ctx, s3Client, bucketName)
	if err != nil {
		log.Printf("Warning: Unable to get tags for bucket %s: %v", bucketName, err)
	}
	bucket.Tags = tags

	// Get lifecycle rules
	lifecycleRules, err := getBucketLifecycleRules(ctx, s3Client, bucketName)
	if err != nil {
		log.Printf("Warning: Unable to get lifecycle rules for bucket %s: %v", bucketName, err)
	}
	bucket.LifecycleRules = lifecycleRules

	// Get storage metrics
	size, objectCount, storageClasses, lastModified, err := getBucketStorageMetrics(ctx, s3Client, bucketName)
	if err != nil {
		log.Printf("Warning: Unable to get storage metrics for bucket %s: %v", bucketName, err)
	}
	bucket.SizeBytes = size
	bucket.ObjectCount = objectCount
	bucket.StorageClasses = storageClasses
	bucket.LastModified = lastModified

	// Get access frequency metrics
	accessMetrics, err := getBucketAccessMetrics(ctx, cwClient, bucketName)
	if err != nil {
		log.Printf("Warning: Unable to get access metrics for bucket %s: %v", bucketName, err)
	}
	bucket.AccessFrequency = accessMetrics

	return bucket, nil
}

// getBucketRegion determines the region of a bucket
func getBucketRegion(ctx context.Context, client *s3.Client, bucketName string) (string, error) {
	result, err := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
		Bucket: aws.String(bucketName),
	})

	if err != nil {
		return "", err
	}

	// Map empty location constraint to us-east-1
	if result.LocationConstraint == "" {
		return "us-east-1", nil
	}

	return string(result.LocationConstraint), nil
}

// getBucketTags retrieves tags for a bucket
func getBucketTags(ctx context.Context, client *s3.Client, bucketName string) (map[string]string, error) {
	result, err := client.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{
		Bucket: aws.String(bucketName),
	})

	if err != nil {
		// No tags is a normal condition, not an error
		return make(map[string]string), nil
	}

	tags := make(map[string]string)
	for _, tag := range result.TagSet {
		if tag.Key != nil && tag.Value != nil {
			tags[*tag.Key] = *tag.Value
		}
	}

	return tags, nil
}

// getBucketLifecycleRules retrieves and simplifies lifecycle rules
func getBucketLifecycleRules(ctx context.Context, client *s3.Client, bucketName string) ([]LifecycleRuleInfo, error) {
	result, err := client.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{
		Bucket: aws.String(bucketName),
	})

	if err != nil {
		// No lifecycle rules is a normal condition, not an error
		return []LifecycleRuleInfo{}, nil
	}

	rules := make([]LifecycleRuleInfo, 0, len(result.Rules))

	for _, rule := range result.Rules {
		// Skip rules that don't have an ID
		if rule.ID == nil {
			continue
		}

		ruleInfo := LifecycleRuleInfo{
			ID:     *rule.ID,
			Status: string(rule.Status),
		}

		// Check for transitions
		if len(rule.Transitions) > 0 {
			ruleInfo.HasTransitions = true

			// Find the earliest transition
			minDays := 999999
			for _, transition := range rule.Transitions {
				if transition.Days > 0 && int(transition.Days) < minDays {
					minDays = int(transition.Days)
				}
			}

			if minDays < 999999 {
				ruleInfo.ObjectAgeThreshold = minDays
			}
		}

		// Check for expirations
		if rule.Expiration != nil && rule.Expiration.Days > 0 {
			ruleInfo.HasExpirations = true

			// If no transitions or expiration comes first, use it for threshold
			if !ruleInfo.HasTransitions || int(rule.Expiration.Days) < ruleInfo.ObjectAgeThreshold {
				ruleInfo.ObjectAgeThreshold = int(rule.Expiration.Days)
			}
		}

		rules = append(rules, ruleInfo)
	}

	return rules, nil
}

// getBucketStorageMetrics estimates bucket size and composition by sampling objects
func getBucketStorageMetrics(ctx context.Context, client *s3.Client, bucketName string) (
	size int64,
	objectCount int64,
	storageClasses map[string]int64,
	lastModified time.Time,
	err error,
) {
	storageClasses = make(map[string]int64)
	lastModified = time.Time{}

	// Use LIST to get object counts and sizes by storage class
	// This is a sampling approach to avoid listing all objects in very large buckets
	var continuationToken *string
	maxKeys := int32(1000) // Sample up to 1000 objects per request
	sampleSize := 0

	for {
		listParams := &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucketName),
			MaxKeys:           maxKeys,
			ContinuationToken: continuationToken,
		}

		listResult, listErr := client.ListObjectsV2(ctx, listParams)
		if listErr != nil {
			return 0, 0, storageClasses, lastModified, listErr
		}

		// Process objects
		for _, obj := range listResult.Contents {
			size += obj.Size
			objectCount++

			// Track storage classes
			storageClass := string(obj.StorageClass)
			if storageClass == "" {
				storageClass = "STANDARD" // Default storage class
			}
			storageClasses[storageClass] += obj.Size

			// Track the most recent object modification
			if obj.LastModified != nil && obj.LastModified.After(lastModified) {
				lastModified = *obj.LastModified
			}
		}

		sampleSize += len(listResult.Contents)

		// If we've sampled enough objects or there are no more, break
		if !listResult.IsTruncated || sampleSize >= 5000 {
			break
		}

		continuationToken = listResult.NextContinuationToken
	}

	return size, objectCount, storageClasses, lastModified, nil
}

// getBucketAccessMetrics retrieves access patterns from CloudWatch
func getBucketAccessMetrics(ctx context.Context, client *cloudwatch.Client, bucketName string) (map[string]float64, error) {
	accessFrequency := make(map[string]float64)

	// Define the metrics to retrieve
	operations := []string{
		"GetRequests",
		"PutRequests",
		"DeleteRequests",
	}

	// Calculate time period for metric queries (last 7 days)
	endTime := time.Now()
	startTime := endTime.AddDate(0, 0, -7)

	// Query each operation type
	for _, operation := range operations {
		input := &cloudwatch.GetMetricStatisticsInput{
			Namespace:  aws.String("AWS/S3"),
			MetricName: aws.String(operation),
			Dimensions: []types.Dimension{
				{
					Name:  aws.String("BucketName"),
					Value: aws.String(bucketName),
				},
			},
			StartTime:  aws.Time(startTime),
			EndTime:    aws.Time(endTime),
			Period:     aws.Int32(86400), // 1 day in seconds
			Statistics: []types.Statistic{types.StatisticSum},
		}

		result, err := client.GetMetricStatistics(ctx, input)
		if err != nil {
			return accessFrequency, err
		}

		// Calculate average daily operations
		var totalOperations float64
		for _, datapoint := range result.Datapoints {
			if datapoint.Sum != nil {
				totalOperations += *datapoint.Sum
			}
		}

		// Avoid division by zero
		days := float64(len(result.Datapoints))
		if days > 0 {
			accessFrequency[operation] = totalOperations / days
		} else {
			accessFrequency[operation] = 0
		}
	}

	return accessFrequency, nil
}
