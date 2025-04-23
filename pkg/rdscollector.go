package pkg

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdsTypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
)

// RDSInstance holds metadata and computed metrics for an RDS instance
type RDSInstance struct {
	InstanceID       string            `json:"instanceId"`
	InstanceType     string            `json:"instanceType"`
	Engine           string            `json:"engine"`
	EngineVersion    string            `json:"engineVersion"`
	StorageType      string            `json:"storageType"`
	AllocatedStorage int32             `json:"allocatedStorage"`
	MultiAZ          bool              `json:"multiAZ"`
	LaunchTime       time.Time         `json:"launchTime"`
	Status           string            `json:"status"`
	Region           string            `json:"region"`
	Tags             map[string]string `json:"tags"`
	CPUAvg7d         float64           `json:"cpuAvg7d"`
	ConnectionsAvg7d float64           `json:"connectionsAvg7d"`
	IOPSAvg7d        float64           `json:"iopsAvg7d"`
	StorageUsed      float64           `json:"storageUsed"`
}

// ListRDSInstances retrieves all RDS instances and their key metrics
func ListRDSInstances(
	ctx context.Context,
	rdsClient *rds.Client,
	cwClient *cloudwatch.Client,
	maxInstances int,
) ([]RDSInstance, error) {
	// Get list of RDS instances
	var instances []rdsTypes.DBInstance
	var nextToken *string

	for {
		input := &rds.DescribeDBInstancesInput{
			Marker:     nextToken,
			MaxRecords: aws.Int32(100),
		}

		resp, err := rdsClient.DescribeDBInstances(ctx, input)
		if err != nil {
			return nil, err
		}

		instances = append(instances, resp.DBInstances...)

		// Check if there are more pages
		if resp.Marker == nil {
			break
		}
		nextToken = resp.Marker
	}

	// Apply limit if specified
	if maxInstances > 0 && len(instances) > maxInstances {
		log.Printf("Limiting RDS scan to %d instances (found %d)", maxInstances, len(instances))
		instances = instances[:maxInstances]
	} else {
		log.Printf("Processing %d RDS instances", len(instances))
	}

	// Process instances in parallel with a worker pool
	results := make([]RDSInstance, 0, len(instances))
	resultsMutex := &sync.Mutex{}
	wg := &sync.WaitGroup{}
	semaphore := make(chan struct{}, 5) // Limit to 5 concurrent requests

	for _, instance := range instances {
		wg.Add(1)

		go func(db rdsTypes.DBInstance) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Set a timeout for processing each instance
			instCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			// Collect instance data
			rdsInstance, err := collectRDSInstanceData(instCtx, rdsClient, cwClient, db)
			if err != nil {
				log.Printf("Warning: Error collecting data for RDS instance %s: %v",
					aws.ToString(db.DBInstanceIdentifier), err)
				return
			}

			// Add to results
			resultsMutex.Lock()
			results = append(results, rdsInstance)
			resultsMutex.Unlock()
		}(instance)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	return results, nil
}

// collectRDSInstanceData gathers all relevant data for a single RDS instance
func collectRDSInstanceData(
	ctx context.Context,
	rdsClient *rds.Client,
	cwClient *cloudwatch.Client,
	db rdsTypes.DBInstance,
) (RDSInstance, error) {
	instanceID := aws.ToString(db.DBInstanceIdentifier)

	instance := RDSInstance{
		InstanceID:    instanceID,
		InstanceType:  aws.ToString(db.DBInstanceClass),
		Engine:        aws.ToString(db.Engine),
		EngineVersion: aws.ToString(db.EngineVersion),
		StorageType:   aws.ToString(db.StorageType),
		Status:        aws.ToString(db.DBInstanceStatus),
		Region:        rdsClient.Options().Region,
		Tags:          make(map[string]string),
	}

	// Set allocated storage
	if db.AllocatedStorage != nil {
		instance.AllocatedStorage = *db.AllocatedStorage
	}

	// Set multi-AZ flag
	if db.MultiAZ != nil {
		instance.MultiAZ = *db.MultiAZ
	}

	// Set launch time if available
	if db.InstanceCreateTime != nil {
		instance.LaunchTime = *db.InstanceCreateTime
	}

	// Get instance tags
	tagsInput := &rds.ListTagsForResourceInput{
		ResourceName: db.DBInstanceArn,
	}

	tagsResp, err := rdsClient.ListTagsForResource(ctx, tagsInput)
	if err != nil {
		log.Printf("Warning: Unable to get tags for RDS instance %s: %v", instanceID, err)
	} else {
		for _, tag := range tagsResp.TagList {
			if tag.Key != nil && tag.Value != nil {
				instance.Tags[*tag.Key] = *tag.Value
			}
		}
	}

	// Get CloudWatch metrics
	// Define the metrics to retrieve
	endTime := time.Now().UTC()
	startTime := endTime.AddDate(0, 0, -7) // Last 7 days

	// Get CPU utilization
	cpuAvg, err := getRDSMetric(ctx, cwClient, instanceID, "CPUUtilization", startTime, endTime)
	if err != nil {
		log.Printf("Warning: Unable to get CPU metrics for %s: %v", instanceID, err)
	}
	instance.CPUAvg7d = cpuAvg

	// Get database connections
	connectionsAvg, err := getRDSMetric(ctx, cwClient, instanceID, "DatabaseConnections", startTime, endTime)
	if err != nil {
		log.Printf("Warning: Unable to get connections metrics for %s: %v", instanceID, err)
	}
	instance.ConnectionsAvg7d = connectionsAvg

	// Get IOPS (Read + Write)
	readIOPSAvg, err := getRDSMetric(ctx, cwClient, instanceID, "ReadIOPS", startTime, endTime)
	if err != nil {
		log.Printf("Warning: Unable to get Read IOPS metrics for %s: %v", instanceID, err)
	}

	writeIOPSAvg, err := getRDSMetric(ctx, cwClient, instanceID, "WriteIOPS", startTime, endTime)
	if err != nil {
		log.Printf("Warning: Unable to get Write IOPS metrics for %s: %v", instanceID, err)
	}

	instance.IOPSAvg7d = readIOPSAvg + writeIOPSAvg

	// Get storage used percentage
	storageUsed, err := getRDSMetric(ctx, cwClient, instanceID, "FreeStorageSpace", startTime, endTime)
	if err != nil {
		log.Printf("Warning: Unable to get storage metrics for %s: %v", instanceID, err)
	} else if instance.AllocatedStorage > 0 {
		// Convert from free bytes to used percentage
		allocatedBytes := float64(instance.AllocatedStorage) * 1024 * 1024 * 1024 // GiB to bytes
		instance.StorageUsed = 100.0 - ((storageUsed / allocatedBytes) * 100.0)

		// Clamp to valid range
		if instance.StorageUsed < 0 {
			instance.StorageUsed = 0
		} else if instance.StorageUsed > 100 {
			instance.StorageUsed = 100
		}
	}

	return instance, nil
}

// getRDSMetric retrieves a specific CloudWatch metric for an RDS instance
func getRDSMetric(
	ctx context.Context,
	cwClient *cloudwatch.Client,
	instanceID, metricName string,
	startTime, endTime time.Time,
) (float64, error) {
	input := &cloudwatch.GetMetricStatisticsInput{
		Namespace:  aws.String("AWS/RDS"),
		MetricName: aws.String(metricName),
		Dimensions: []types.Dimension{{
			Name:  aws.String("DBInstanceIdentifier"),
			Value: aws.String(instanceID),
		}},
		StartTime:  &startTime,
		EndTime:    &endTime,
		Period:     aws.Int32(3600), // 1 hour granularity
		Statistics: []types.Statistic{types.StatisticAverage},
	}

	resp, err := cwClient.GetMetricStatistics(ctx, input)
	if err != nil {
		return 0, err
	}

	// Calculate average from datapoints
	var sum float64
	for _, dp := range resp.Datapoints {
		sum += *dp.Average
	}

	// Avoid division by zero if no datapoints
	count := float64(len(resp.Datapoints))
	if count == 0 {
		return 0, nil
	}

	return sum / count, nil
}
