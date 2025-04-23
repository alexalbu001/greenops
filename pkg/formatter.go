package pkg

import (
	"encoding/json"
	"fmt"
	"io"
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

func FormatAnalysisReport(w io.Writer, report []ReportItem, colorize bool) {
	// Header
	printHeader(w, "GreenOps Analysis Report", colorize)
	fmt.Fprintf(w, "Generated: %s\n", time.Now().Format(time.RFC1123))

	// Count resource types
	ec2Count := 0
	s3Count := 0
	for _, item := range report {
		if item.Instance.InstanceID != "" {
			ec2Count++
		} else if item.S3Bucket.BucketName != "" {
			s3Count++
		}
	}

	// Summary
	if ec2Count > 0 {
		fmt.Fprintf(w, "EC2 instances analyzed: %d\n", ec2Count)
	}
	if s3Count > 0 {
		fmt.Fprintf(w, "S3 buckets analyzed: %d\n", s3Count)
	}
	fmt.Fprintf(w, "Total resources analyzed: %d\n\n", len(report))

	// EC2 summary
	if ec2Count > 0 {
		printEC2Summary(w, report, colorize)
	}

	// S3 summary
	if s3Count > 0 {
		printS3Summary(w, report, colorize)
	}

	// Detailed analysis: EC2 first, then S3
	var ordered []ReportItem
	for _, item := range report {
		if item.Instance.InstanceID != "" {
			ordered = append(ordered, item)
		}
	}
	for _, item := range report {
		if item.S3Bucket.BucketName != "" {
			ordered = append(ordered, item)
		}
	}

	for i, item := range ordered {
		if item.Instance.InstanceID != "" {
			printInstanceAnalysis(w, i+1, item, colorize)
		} else if item.S3Bucket.BucketName != "" {
			printS3BucketAnalysis(w, i+1, item, colorize)
		}
	}
}

// IsEmpty checks if a struct is empty
func IsEmpty(obj interface{}) bool {
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
func printEC2Summary(w io.Writer, report []ReportItem, colorize bool) {
	var ec2Items []ReportItem
	for _, item := range report {
		if item.Instance.InstanceID != "" {
			ec2Items = append(ec2Items, item)
		}
	}
	if len(ec2Items) == 0 {
		return
	}
	if colorize {
		fmt.Fprintf(w, "\n%sEC2 INSTANCES SUMMARY%s\n", ColorBold+ColorBlue, ColorReset)
		fmt.Fprintln(w, strings.Repeat("-", 22))
	} else {
		fmt.Fprintln(w, "\nEC2 INSTANCES SUMMARY")
		fmt.Fprintln(w, strings.Repeat("-", 22))
	}
	headers := []string{"INSTANCE ID", "TYPE", "CPU AVG", "STATUS"}
	fmt.Fprintf(w, "%-15s %-10s %-10s %-15s\n", headers[0], headers[1], headers[2], headers[3])
	fmt.Fprintln(w, strings.Repeat("-", 55))
	for _, item := range ec2Items {
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
		fmt.Fprintf(w, "%-15s %-10s %-10.1f%% %-15s\n",
			item.Instance.InstanceID,
			item.Instance.InstanceType,
			item.Instance.CPUAvg7d,
			statusText)
	}
	fmt.Fprintln(w)
}

// printS3Summary prints a summary table of S3 buckets
func printS3Summary(w io.Writer, report []ReportItem, colorize bool) {
	var s3Items []ReportItem
	for _, item := range report {
		if item.S3Bucket.BucketName != "" {
			s3Items = append(s3Items, item)
		}
	}
	if len(s3Items) == 0 {
		return
	}
	if colorize {
		fmt.Fprintf(w, "\n%sS3 BUCKETS SUMMARY%s\n", ColorBold+ColorBlue, ColorReset)
		fmt.Fprintln(w, strings.Repeat("-", 18))
	} else {
		fmt.Fprintln(w, "\nS3 BUCKETS SUMMARY")
		fmt.Fprintln(w, strings.Repeat("-", 18))
	}
	headers := []string{"BUCKET NAME", "SIZE (GB)", "OBJECTS", "LIFECYCLE"}
	fmt.Fprintf(w, "%-30s %-10s %-10s %-15s\n", headers[0], headers[1], headers[2], headers[3])
	fmt.Fprintln(w, strings.Repeat("-", 70))
	for _, item := range s3Items {
		expStatus := "MISSING"
		if len(item.S3Bucket.LifecycleRules) > 0 {
			has := false
			for _, r := range item.S3Bucket.LifecycleRules {
				if r.Status == "Enabled" {
					has = true
					break
				}
			}
			if has {
				expStatus = "CONFIGURED"
			} else {
				expStatus = "DISABLED"
			}
		}
		statusText := expStatus
		if colorize {
			switch expStatus {
			case "MISSING":
				statusText = ColorRed + expStatus + ColorReset
			case "DISABLED":
				statusText = ColorYellow + expStatus + ColorReset
			case "CONFIGURED":
				statusText = ColorGreen + expStatus + ColorReset
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

// printInstanceAnalysis prints detailed analysis for an EC2 instance
func printInstanceAnalysis(w io.Writer, index int, item ReportItem, colorize bool) {
	// Section header
	title := fmt.Sprintf("Instance %d: %s (%s)", index, item.Instance.InstanceID, item.Instance.InstanceType)
	if colorize {
		fmt.Fprintf(w, "\n%s%s%s\n", ColorBold+ColorBlue, title, ColorReset)
		fmt.Fprintln(w, strings.Repeat("-", len(title)))
	} else {
		fmt.Fprintf(w, "\n%s\n", title)
		fmt.Fprintln(w, strings.Repeat("-", len(title)))
	}

	// Instance metadata
	fmt.Fprintf(w, "Launch Time: %s\n", item.Instance.LaunchTime.Format(time.RFC3339))
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

// printS3BucketAnalysis prints detailed analysis for an S3 bucket
func printS3BucketAnalysis(w io.Writer, index int, item ReportItem, colorize bool) {
	// Section header
	title := fmt.Sprintf("S3 Bucket %d: %s", index, item.S3Bucket.BucketName)
	if colorize {
		fmt.Fprintf(w, "\n%s%s%s\n", ColorBold+ColorBlue, title, ColorReset)
		fmt.Fprintln(w, strings.Repeat("-", len(title)))
	} else {
		fmt.Fprintf(w, "\n%s\n", title)
		fmt.Fprintln(w, strings.Repeat("-", len(title)))
	}

	// Bucket metadata
	fmt.Fprintf(w, "Region: %s\n", item.S3Bucket.Region)
	fmt.Fprintf(w, "Creation Date: %s\n", item.S3Bucket.CreationDate.Format(time.RFC3339))
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
