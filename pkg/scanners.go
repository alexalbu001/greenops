package pkg

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ResourceScanner is the interface all resource scanners must implement
type ResourceScanner interface {
	// Scan returns a slice of resources and any error encountered
	Scan(ctx context.Context) (interface{}, error)
	// Name returns the name of the resource type
	Name() string
}

// EC2Scanner scans EC2 instances
type EC2Scanner struct {
	EC2Client *ec2.Client
	CWClient  *cloudwatch.Client
	DaysBack  int
	MaxItems  int
}

// Scan implements ResourceScanner interface
func (s *EC2Scanner) Scan(ctx context.Context) (interface{}, error) {
	log.Printf("Scanning EC2 instances (past %d days)...", s.DaysBack)
	instances, err := ListInstances(ctx, s.EC2Client, s.CWClient)
	if err != nil {
		return nil, err
	}

	// Apply limit if specified
	if s.MaxItems > 0 && len(instances) > s.MaxItems {
		log.Printf("Limiting EC2 scan to %d instances (found %d)", s.MaxItems, len(instances))
		instances = instances[:s.MaxItems]
	}

	return instances, nil
}

// Name implements ResourceScanner interface
func (s *EC2Scanner) Name() string {
	return "ec2"
}

// S3Scanner scans S3 buckets
type S3Scanner struct {
	S3Client *s3.Client
	CWClient *cloudwatch.Client
	MaxItems int
}

// Scan implements ResourceScanner interface
func (s *S3Scanner) Scan(ctx context.Context) (interface{}, error) {
	log.Printf("Scanning S3 buckets...")
	buckets, err := ListBuckets(ctx, s.S3Client, s.CWClient, s.MaxItems)
	if err != nil {
		return nil, err
	}

	log.Printf("S3 scan completed: found %d buckets", len(buckets))
	return buckets, nil
}

// Name implements ResourceScanner interface
func (s *S3Scanner) Name() string {
	return "s3"
}

// EBSScanner scans EBS volumes (placeholder for future implementation)
type EBSScanner struct {
	EC2Client *ec2.Client
	CWClient  *cloudwatch.Client
	DaysBack  int
}

// Scan implements ResourceScanner interface
func (s *EBSScanner) Scan(ctx context.Context) (interface{}, error) {
	log.Println("EBS volume scanning not yet implemented")
	return nil, fmt.Errorf("EBS scanning not implemented")
}

// Name implements ResourceScanner interface
func (s *EBSScanner) Name() string {
	return "ebs"
}

// RDSScanner scans RDS instances (placeholder for future implementation)
type RDSScanner struct {
	RDSClient *rds.Client
	CWClient  *cloudwatch.Client
	DaysBack  int
}

// Scan implements ResourceScanner interface
func (s *RDSScanner) Scan(ctx context.Context) (interface{}, error) {
	log.Println("RDS instance scanning not yet implemented")
	return nil, fmt.Errorf("RDS scanning not implemented")
}

// Name implements ResourceScanner interface
func (s *RDSScanner) Name() string {
	return "rds"
}

// ScanResources scans multiple resource types in parallel
func ScanResources(ctx context.Context, cfg aws.Config, resourceTypes []string, maxItems int, daysBack int) (map[string]interface{}, error) {
	results := make(map[string]interface{})

	// Early return if no resource types specified
	if len(resourceTypes) == 0 {
		return results, nil
	}

	// Create clients
	ec2Client := ec2.NewFromConfig(cfg)
	cwClient := cloudwatch.NewFromConfig(cfg)
	rdsClient := rds.NewFromConfig(cfg)
	s3Client := s3.NewFromConfig(cfg)

	// Create scanners map
	scanners := map[string]ResourceScanner{
		"ec2": &EC2Scanner{
			EC2Client: ec2Client,
			CWClient:  cwClient,
			DaysBack:  daysBack,
			MaxItems:  maxItems,
		},
		"ebs": &EBSScanner{
			EC2Client: ec2Client,
			CWClient:  cwClient,
			DaysBack:  daysBack,
		},
		"rds": &RDSScanner{
			RDSClient: rdsClient,
			CWClient:  cwClient,
			DaysBack:  daysBack,
		},
		"s3": &S3Scanner{
			S3Client: s3Client,
			CWClient: cwClient,
			MaxItems: maxItems,
		},
	}

	// Filter scanners to requested resource types
	var selectedScanners []ResourceScanner
	for _, resType := range resourceTypes {
		if scanner, ok := scanners[resType]; ok {
			selectedScanners = append(selectedScanners, scanner)
		} else {
			log.Printf("Warning: Unknown resource type '%s'", resType)
		}
	}

	// Early return if no valid resource types
	if len(selectedScanners) == 0 {
		return results, fmt.Errorf("no valid resource types specified")
	}

	// Run scanners in parallel
	var wg sync.WaitGroup
	var mu sync.Mutex
	errCount := 0

	for _, scanner := range selectedScanners {
		wg.Add(1)
		go func(s ResourceScanner) {
			defer wg.Done()

			// Create timeout context for this scan
			scanCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()

			// Run the scan
			result, err := s.Scan(scanCtx)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				log.Printf("Error scanning %s: %v", s.Name(), err)
				errCount++
			} else if result != nil {
				results[s.Name()] = result
			}
		}(scanner)
	}

	// Wait for all scanners to complete
	wg.Wait()

	// Return error if all scanners failed
	if errCount == len(selectedScanners) {
		return results, fmt.Errorf("all resource scans failed")
	}

	return results, nil
}
