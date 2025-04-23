package pkg

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// ConsoleColors for terminal output
const (
	ColorReset   = "\033[0m"
	ColorRed     = "\033[31m"
	ColorGreen   = "\033[32m"
	ColorYellow  = "\033[33m"
	ColorBlue    = "\033[34m"
	ColorMagenta = "\033[35m"
	ColorCyan    = "\033[36m"
	ColorWhite   = "\033[37m"
	ColorBold    = "\033[1m"
)

// FormatAnalysisReport prints the analysis results in a user-friendly format
func FormatAnalysisReport(w io.Writer, report []ReportItem, colorize bool) {
	// Header
	printHeader(w, "GreenOps Analysis Report", colorize)
	fmt.Fprintf(w, "Generated: %s\n", time.Now().Format(time.RFC1123))

	// Pre-process and separate resources by type
	var ec2Items []ReportItem
	var s3Items []ReportItem

	// Debug counter for validating resources
	ec2Count := 0
	s3Count := 0
	unknownCount := 0

	// Explicitly separate resources by type
	for _, item := range report {
		resourceType := item.GetResourceType()

		// Debug logging of resource types
		if resourceType == ResourceTypeEC2 {
			ec2Count++
			if !isEmptyStruct(item.Instance) && item.Instance.InstanceID != "" {
				ec2Items = append(ec2Items, item)
			}
		} else if resourceType == ResourceTypeS3 {
			s3Count++
			if !isEmptyStruct(item.S3Bucket) && item.S3Bucket.BucketName != "" {
				s3Items = append(s3Items, item)
			}
		} else {
			unknownCount++

			// Try to infer type from analysis text
			if strings.Contains(item.Analysis, "S3 Bucket Analysis") {
				// Extract bucket name from analysis if possible
				bucketName := extractBucketName(item.Analysis)
				if bucketName != "" {
					// Create a proper S3Bucket structure
					s3Bucket := S3Bucket{
						BucketName: bucketName,
					}

					// Skip adding if there's no bucket name
					if bucketName != "" {
						newItem := item
						newItem.S3Bucket = s3Bucket
						newItem.Instance = Instance{} // Clear instance data
						s3Items = append(s3Items, newItem)
						s3Count++
					}
				}
			} else if strings.Contains(item.Analysis, "EC2 Instance Analysis") {
				// Extract instance ID from analysis if possible
				instanceID := extractInstanceID(item.Analysis)
				if instanceID != "" {
					// Create proper Instance structure
					instance := Instance{
						InstanceID: instanceID,
					}

					newItem := item
					newItem.Instance = instance
					newItem.S3Bucket = S3Bucket{} // Clear bucket data
					ec2Items = append(ec2Items, newItem)
					ec2Count++
				}
			}
		}
	}

	// Print resource counts
	ec2DisplayCount := len(ec2Items)
	s3DisplayCount := len(s3Items)

	if ec2DisplayCount > 0 {
		fmt.Fprintf(w, "EC2 instances analyzed: %d\n", ec2DisplayCount)
	}
	if s3DisplayCount > 0 {
		fmt.Fprintf(w, "S3 buckets analyzed: %d\n", s3DisplayCount)
	}
	fmt.Fprintf(w, "Total resources analyzed: %d\n", ec2DisplayCount+s3DisplayCount)

	// Print EC2 summary if there are any instances
	if len(ec2Items) > 0 {
		printEC2Summary(w, ec2Items, colorize)
	}

	// Print S3 summary if there are any buckets
	if len(s3Items) > 0 {
		printS3Summary(w, s3Items, colorize)
	}

	// Print EC2 instance details
	if len(ec2Items) > 0 {
		printEC2DetailsHeader(w, colorize)

		// Sort instances by ID for consistent display
		sort.Slice(ec2Items, func(i, j int) bool {
			return ec2Items[i].Instance.InstanceID < ec2Items[j].Instance.InstanceID
		})

		for i, item := range ec2Items {
			printEC2Details(w, i+1, item, colorize)
		}
	}

	// Print S3 bucket details
	if len(s3Items) > 0 {
		printS3DetailsHeader(w, colorize)

		// Sort buckets by name for consistent display
		sort.Slice(s3Items, func(i, j int) bool {
			return s3Items[i].S3Bucket.BucketName < s3Items[j].S3Bucket.BucketName
		})

		for i, item := range s3Items {
			printS3Details(w, i+1, item, colorize)
		}
	}
}

// Utility functions for extracting information from analysis text
func extractBucketName(analysis string) string {
	// Look for "S3 Bucket Analysis: BUCKET_NAME" pattern
	if strings.Contains(analysis, "S3 Bucket Analysis:") {
		parts := strings.Split(analysis, "S3 Bucket Analysis:")
		if len(parts) > 1 {
			namePart := strings.TrimSpace(parts[1])
			endPos := strings.Index(namePart, "\n")
			if endPos > 0 {
				return strings.TrimSpace(namePart[:endPos])
			}
			return namePart
		}
	}

	// Look for "Name: BUCKET_NAME" pattern
	if strings.Contains(analysis, "**Name**:") {
		parts := strings.Split(analysis, "**Name**:")
		if len(parts) > 1 {
			namePart := strings.TrimSpace(parts[1])
			endPos := strings.Index(namePart, "\n")
			if endPos > 0 {
				return strings.TrimSpace(namePart[:endPos])
			}
		}
	}

	return ""
}

func extractInstanceID(analysis string) string {
	// Look for "Instance ID: i-XXXXXXXXXX" pattern
	if strings.Contains(analysis, "**Instance ID**:") {
		parts := strings.Split(analysis, "**Instance ID**:")
		if len(parts) > 1 {
			idPart := strings.TrimSpace(parts[1])
			endPos := strings.Index(idPart, "\n")
			if endPos > 0 {
				return strings.TrimSpace(idPart[:endPos])
			}
		}
	}

	// Look for "ID: i-XXXXXXXXXX" pattern
	if strings.Contains(analysis, "**ID**:") {
		parts := strings.Split(analysis, "**ID**:")
		if len(parts) > 1 {
			idPart := strings.TrimSpace(parts[1])
			endPos := strings.Index(idPart, "\n")
			if endPos > 0 {
				id := strings.TrimSpace(idPart[:endPos])
				if strings.HasPrefix(id, "i-") {
					return id
				}
			}
		}
	}

	return ""
}

// isEmptyStruct checks if a struct is empty (renamed to avoid conflict with IsEmptyObject in jobs.go)
func isEmptyStruct(obj interface{}) bool {
	// Simple check - this would need to be more robust in production
	jsonData, err := json.Marshal(obj)
	if err != nil {
		return true
	}
	return string(jsonData) == "{}" || string(jsonData) == "null"
}

func printHeader(w io.Writer, title string, colorize bool) {
	if colorize {
		fmt.Fprintf(w, "%s%s%s\n", ColorBold+ColorGreen, title, ColorReset)
		fmt.Fprintln(w, strings.Repeat("=", len(title)))
	} else {
		fmt.Fprintln(w, title)
		fmt.Fprintln(w, strings.Repeat("=", len(title)))
	}
}

// printEC2Summary prints a summary table of EC2 instances
func printEC2Summary(w io.Writer, ec2Items []ReportItem, colorize bool) {
	if len(ec2Items) == 0 {
		return
	}

	// Print section header
	if colorize {
		fmt.Fprintf(w, "\n%sEC2 INSTANCES SUMMARY%s\n", ColorBold+ColorBlue, ColorReset)
		fmt.Fprintln(w, strings.Repeat("-", 22))
	} else {
		fmt.Fprintln(w, "\nEC2 INSTANCES SUMMARY")
		fmt.Fprintln(w, strings.Repeat("-", 22))
	}

	// Print table header
	headers := []string{"INSTANCE ID", "TYPE", "CPU AVG", "STATUS"}
	fmt.Fprintf(w, "%-20s %-10s %-10s %-15s\n", headers[0], headers[1], headers[2], headers[3])
	fmt.Fprintln(w, strings.Repeat("-", 60))

	// Print table rows - only for valid EC2 instances
	for _, item := range ec2Items {
		if item.Instance.InstanceID == "" {
			continue // Skip entries without an instance ID
		}

		status := getEfficiencyStatus(item.Instance.CPUAvg7d)
		statusText := status

		if colorize {
			switch status {
			case "CRITICAL":
				statusText = ColorRed + status + ColorReset
			case "WARNING":
				statusText = ColorYellow + status + ColorReset
			case "GOOD":
				statusText = ColorGreen + status + ColorReset
			}
		}

		fmt.Fprintf(w, "%-20s %-10s %-10.1f%% %-15s\n",
			item.Instance.InstanceID,
			item.Instance.InstanceType,
			item.Instance.CPUAvg7d,
			statusText)
	}
	fmt.Fprintln(w)
}

// printS3Summary prints a summary table of S3 buckets
func printS3Summary(w io.Writer, s3Items []ReportItem, colorize bool) {
	if len(s3Items) == 0 {
		return
	}

	// Print section header
	if colorize {
		fmt.Fprintf(w, "\n%sS3 BUCKETS SUMMARY%s\n", ColorBold+ColorBlue, ColorReset)
		fmt.Fprintln(w, strings.Repeat("-", 18))
	} else {
		fmt.Fprintln(w, "\nS3 BUCKETS SUMMARY")
		fmt.Fprintln(w, strings.Repeat("-", 18))
	}

	// Print table header
	headers := []string{"BUCKET NAME", "SIZE (GB)", "OBJECTS", "LIFECYCLE"}
	fmt.Fprintf(w, "%-30s %-10s %-10s %-15s\n", headers[0], headers[1], headers[2], headers[3])
	fmt.Fprintln(w, strings.Repeat("-", 70))

	// Print table rows - only for valid S3 buckets
	for _, item := range s3Items {
		if item.S3Bucket.BucketName == "" {
			continue // Skip entries without a bucket name
		}

		// Determine lifecycle status
		lifecycleStatus := "MISSING"
		if len(item.S3Bucket.LifecycleRules) > 0 {
			hasEnabledRules := false
			for _, rule := range item.S3Bucket.LifecycleRules {
				if rule.Status == "Enabled" {
					hasEnabledRules = true
					break
				}
			}

			if hasEnabledRules {
				lifecycleStatus = "CONFIGURED"
			} else {
				lifecycleStatus = "DISABLED"
			}
		}

		statusText := lifecycleStatus

		if colorize {
			switch lifecycleStatus {
			case "MISSING":
				statusText = ColorRed + statusText + ColorReset
			case "DISABLED":
				statusText = ColorYellow + statusText + ColorReset
			case "CONFIGURED":
				statusText = ColorGreen + statusText + ColorReset
			}
		}

		fmt.Fprintf(w, "%-30s %-10.2f %-10d %-15s\n",
			item.S3Bucket.BucketName,
			float64(item.S3Bucket.SizeBytes)/(1024*1024*1024),
			item.S3Bucket.ObjectCount,
			statusText)
	}
	fmt.Fprintln(w)
}

// Print EC2 details section header
func printEC2DetailsHeader(w io.Writer, colorize bool) {
	if colorize {
		fmt.Fprintf(w, "\n%sEC2 INSTANCE DETAILS%s\n", ColorBold+ColorBlue, ColorReset)
		fmt.Fprintln(w, strings.Repeat("=", 19))
	} else {
		fmt.Fprintln(w, "\nEC2 INSTANCE DETAILS")
		fmt.Fprintln(w, strings.Repeat("=", 19))
	}
}

// Print S3 details section header
func printS3DetailsHeader(w io.Writer, colorize bool) {
	if colorize {
		fmt.Fprintf(w, "\n%sS3 BUCKET DETAILS%s\n", ColorBold+ColorBlue, ColorReset)
		fmt.Fprintln(w, strings.Repeat("=", 16))
	} else {
		fmt.Fprintln(w, "\nS3 BUCKET DETAILS")
		fmt.Fprintln(w, strings.Repeat("=", 16))
	}
}

// printEC2Details prints detailed analysis for an EC2 instance
func printEC2Details(w io.Writer, index int, item ReportItem, colorize bool) {
	// Section header
	instanceType := item.Instance.InstanceType
	if instanceType == "" {
		instanceType = "unknown"
	}

	title := fmt.Sprintf("Instance %d: %s (%s)", index, item.Instance.InstanceID, instanceType)
	if colorize {
		fmt.Fprintf(w, "\n%s%s%s\n", ColorBold+ColorBlue, title, ColorReset)
		fmt.Fprintln(w, strings.Repeat("-", len(title)))
	} else {
		fmt.Fprintf(w, "\n%s\n", title)
		fmt.Fprintln(w, strings.Repeat("-", len(title)))
	}

	// Instance metadata - only print if we have actual data
	if !item.Instance.LaunchTime.IsZero() {
		fmt.Fprintf(w, "Launch Time: %s\n", item.Instance.LaunchTime.Format(time.RFC3339))
	}

	fmt.Fprintf(w, "CPU Utilization (7-day avg): %.1f%%\n", item.Instance.CPUAvg7d)

	// Tags
	if len(item.Instance.Tags) > 0 {
		fmt.Fprintln(w, "Tags:")
		for k, v := range item.Instance.Tags {
			fmt.Fprintf(w, "  %s: %s\n", k, v)
		}
	}

	// Analysis (keep original formatting)
	fmt.Fprintln(w, "\nANALYSIS:")
	fmt.Fprintln(w, item.Analysis)
}

// printS3Details prints detailed analysis for an S3 bucket
func printS3Details(w io.Writer, index int, item ReportItem, colorize bool) {
	// Section header
	title := fmt.Sprintf("Bucket %d: %s", index, item.S3Bucket.BucketName)
	if colorize {
		fmt.Fprintf(w, "\n%s%s%s\n", ColorBold+ColorBlue, title, ColorReset)
		fmt.Fprintln(w, strings.Repeat("-", len(title)))
	} else {
		fmt.Fprintf(w, "\n%s\n", title)
		fmt.Fprintln(w, strings.Repeat("-", len(title)))
	}

	// Bucket metadata - only print if we have actual data
	if item.S3Bucket.Region != "" {
		fmt.Fprintf(w, "Region: %s\n", item.S3Bucket.Region)
	}

	if !item.S3Bucket.CreationDate.IsZero() {
		fmt.Fprintf(w, "Creation Date: %s\n", item.S3Bucket.CreationDate.Format(time.RFC3339))
	}

	fmt.Fprintf(w, "Size: %.2f GB\n", float64(item.S3Bucket.SizeBytes)/(1024*1024*1024))
	fmt.Fprintf(w, "Object Count: %d\n", item.S3Bucket.ObjectCount)

	// Last modified time if available
	if !item.S3Bucket.LastModified.IsZero() {
		fmt.Fprintf(w, "Last Modified: %s\n", item.S3Bucket.LastModified.Format(time.RFC3339))
	}

	// Storage class breakdown
	if len(item.S3Bucket.StorageClasses) > 0 {
		fmt.Fprintln(w, "\nStorage Classes:")
		for class, size := range item.S3Bucket.StorageClasses {
			percentage := 0.0
			if item.S3Bucket.SizeBytes > 0 {
				percentage = float64(size) / float64(item.S3Bucket.SizeBytes) * 100
			}
			fmt.Fprintf(w, "  %s: %.2f GB (%.1f%%)\n",
				class, float64(size)/(1024*1024*1024), percentage)
		}
	}

	// Access patterns
	if len(item.S3Bucket.AccessFrequency) > 0 {
		fmt.Fprintln(w, "\nAccess Patterns (daily average):")
		for op, count := range item.S3Bucket.AccessFrequency {
			fmt.Fprintf(w, "  %s: %.1f\n", op, count)
		}
	}

	// Lifecycle rules
	if len(item.S3Bucket.LifecycleRules) > 0 {
		fmt.Fprintln(w, "\nLifecycle Rules:")
		for _, rule := range item.S3Bucket.LifecycleRules {
			ruleStatus := "Disabled"
			if rule.Status == "Enabled" {
				ruleStatus = "Enabled"
			}

			fmt.Fprintf(w, "  Rule '%s' (%s): ", rule.ID, ruleStatus)

			if rule.HasTransitions {
				fmt.Fprintf(w, "Transitions at %d days", rule.ObjectAgeThreshold)
			} else {
				fmt.Fprintf(w, "No transitions")
			}

			if rule.HasExpirations {
				fmt.Fprintf(w, ", Expires at %d days", rule.ObjectAgeThreshold)
			}
			fmt.Fprintln(w)
		}
	} else {
		fmt.Fprintln(w, "\nLifecycle Rules: None configured")
	}

	// Tags
	if len(item.S3Bucket.Tags) > 0 {
		fmt.Fprintln(w, "\nTags:")
		for k, v := range item.S3Bucket.Tags {
			fmt.Fprintf(w, "  %s: %s\n", k, v)
		}
	}

	// Analysis (keep original formatting)
	fmt.Fprintln(w, "\nANALYSIS:")
	fmt.Fprintln(w, item.Analysis)
}

// getEfficiencyStatus returns a status based on CPU utilization
func getEfficiencyStatus(cpuAvg float64) string {
	if cpuAvg < 5 {
		return "CRITICAL"
	} else if cpuAvg < 20 {
		return "WARNING"
	} else {
		return "GOOD"
	}
}
