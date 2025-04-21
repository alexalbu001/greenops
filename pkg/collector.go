package pkg

import (
	"context"
	"log"
	"time"

	// AWS SDK v2 modules
	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2Types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwTypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// Instance holds metadata and computed metrics for an EC2 instance
// - InstanceID: the unique identifier
// - InstanceType: the EC2 flavor (e.g., t3.large)
// - LaunchTime: when the instance was started
// - Tags: key/value metadata attached to the instance
// - CPUAvg7d: calculated 7-day average CPU utilization
type Instance struct {
	InstanceID   string            `json:"instanceId"`
	InstanceType string            `json:"instanceType"`
	LaunchTime   time.Time         `json:"launchTime"`
	Tags         map[string]string `json:"tags"`
	CPUAvg7d     float64           `json:"cpuAvg7d"`
}

// listInstances retrieves all running EC2 instances and calculates their 7-day avg CPU utilization
func ListInstances(
	ctx context.Context,
	ec2Client *ec2.Client,
	cwClient *cloudwatch.Client,
) ([]Instance, error) {
	// DescribeInstancesInput with filter: only "running" state
	input := &ec2.DescribeInstancesInput{
		Filters: []ec2Types.Filter{{
			Name:   aws.String("instance-state-name"),
			Values: []string{"running"},
		}},
	}

	// Call EC2 DescribeInstances API
	resp, err := ec2Client.DescribeInstances(ctx, input)
	if err != nil {
		return nil, err
	}

	var results []Instance

	// Define time window for metrics: last 7 days
	endTime := time.Now().UTC()
	startTime := endTime.AddDate(0, 0, -7)

	// Iterate over reservations (group of instances)
	for _, reservation := range resp.Reservations {
		for _, ec2Inst := range reservation.Instances {
			// Fetch average CPU utilization for this instance
			avgCPU, err := getCPUAvg(ctx, cwClient, *ec2Inst.InstanceId, startTime, endTime)
			if err != nil {
				// Log a warning and continue processing other instances
				log.Printf("warning: unable to fetch CPU metrics for %s: %v", *ec2Inst.InstanceId, err)
			}

			// Convert AWS Tag slice to a simple map for easier lookup
			tags := parseTags(ec2Inst.Tags)

			// Assemble data into our Instance struct
			instance := Instance{
				InstanceID:   *ec2Inst.InstanceId,
				InstanceType: string(ec2Inst.InstanceType),
				LaunchTime:   *ec2Inst.LaunchTime,
				Tags:         tags,
				CPUAvg7d:     avgCPU,
			}

			// Add to results slice
			results = append(results, instance)
		}
	}

	return results, nil
}

// getCPUAvg retrieves CPUUtilization datapoints from CloudWatch and computes an average value
func getCPUAvg(
	ctx context.Context,
	cwClient *cloudwatch.Client,
	instanceID string,
	start, end time.Time,
) (float64, error) {
	// Prepare CloudWatch request: CPUUtilization metric, 1-hour period
	input := &cloudwatch.GetMetricStatisticsInput{
		Namespace:  aws.String("AWS/EC2"),        // Service namespace
		MetricName: aws.String("CPUUtilization"), // Target metric
		Dimensions: []cwTypes.Dimension{{ // Filter by InstanceId
			Name:  aws.String("InstanceId"),
			Value: aws.String(instanceID),
		}},
		StartTime:  &start,                                        // Start of time range
		EndTime:    &end,                                          // End of time range
		Period:     aws.Int32(3600),                               // 3600s = 1 hour granularity
		Statistics: []cwTypes.Statistic{cwTypes.StatisticAverage}, // Fetch average values only
	}

	// Execute the CloudWatch API call
	resp, err := cwClient.GetMetricStatistics(ctx, input)
	if err != nil {
		return 0, err // Propagate error
	}

	// Sum up all average datapoints
	var sum float64
	for _, dp := range resp.Datapoints {
		sum += *dp.Average
	}

	// Avoid division by zero if no datapoints returned
	count := float64(len(resp.Datapoints))
	if count == 0 {
		return 0, nil
	}

	// Return computed average CPU utilization
	return sum / count, nil
}

// parseTags converts AWS SDK Tag slice to a map[string]string for simpler access
func parseTags(tags []ec2Types.Tag) map[string]string {
	tagMap := make(map[string]string)
	for _, tag := range tags {
		if tag.Key != nil && tag.Value != nil {
			tagMap[*tag.Key] = *tag.Value
		}
	}
	return tagMap
}
