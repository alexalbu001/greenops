package pkg

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
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
	ColorGrey    = "\033[90m"
)

// FormatAnalysisReport prints the analysis results in a user-friendly format
func FormatAnalysisReport(w io.Writer, report []ReportItem, colorize bool) {
	// Header
	printSustainabilityHeader(w, colorize)
	printHeader(w, "GreenOps Analysis Report", colorize)
	fmt.Fprintf(w, "Generated: %s\n", time.Now().Format(time.RFC1123))
	printSustainabilitySummary(w, report, colorize)
	fmt.Printf("\n")
	// Pre-process and separate resources by type
	var ec2Items []ReportItem
	var s3Items []ReportItem
	var rdsItems []ReportItem

	// Debug counter for validating resources
	ec2Count := 0
	s3Count := 0
	rdsCount := 0
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
		} else if resourceType == ResourceTypeRDS {
			rdsCount++
			if !isEmptyStruct(item.RDSInstance) && item.RDSInstance.InstanceID != "" {
				rdsItems = append(rdsItems, item)
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
						newItem.Instance = Instance{}       // Clear instance data
						newItem.RDSInstance = RDSInstance{} // Clear RDS data
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
					newItem.S3Bucket = S3Bucket{}       // Clear bucket data
					newItem.RDSInstance = RDSInstance{} // Clear RDS data
					ec2Items = append(ec2Items, newItem)
					ec2Count++
				}
			} else if strings.Contains(item.Analysis, "RDS Instance Analysis") {
				// Extract instance ID from analysis if possible
				instanceID := extractRDSInstanceID(item.Analysis)
				if instanceID != "" {
					// Create proper RDS Instance structure
					rdsInstance := RDSInstance{
						InstanceID: instanceID,
					}

					newItem := item
					newItem.RDSInstance = rdsInstance
					newItem.Instance = Instance{} // Clear EC2 data
					newItem.S3Bucket = S3Bucket{} // Clear S3 data
					rdsItems = append(rdsItems, newItem)
					rdsCount++
				}
			}
		}
	}

	// Print resource counts
	ec2DisplayCount := len(ec2Items)
	s3DisplayCount := len(s3Items)
	rdsDisplayCount := len(rdsItems)
	totalCount := ec2DisplayCount + s3DisplayCount + rdsDisplayCount

	if ec2DisplayCount > 0 {
		fmt.Fprintf(w, "EC2 instances analyzed: %d\n", ec2DisplayCount)
	}
	if s3DisplayCount > 0 {
		fmt.Fprintf(w, "S3 buckets analyzed: %d\n", s3DisplayCount)
	}
	if rdsDisplayCount > 0 {
		fmt.Fprintf(w, "RDS instances analyzed: %d\n", rdsDisplayCount)
	}
	fmt.Fprintf(w, "Total resources analyzed: %d\n", totalCount)

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

	// Print RDS instance details
	if len(rdsItems) > 0 {
		printRDSDetailsHeader(w, colorize)

		// Sort instances by ID for consistent display
		sort.Slice(rdsItems, func(i, j int) bool {
			return rdsItems[i].RDSInstance.InstanceID < rdsItems[j].RDSInstance.InstanceID
		})

		for i, item := range rdsItems {
			printRDSDetails(w, i+1, item, colorize)
		}
	}
}

// printSustainabilityHeader prints a banner for sustainability focus
func printSustainabilityHeader(w io.Writer, colorize bool) {
	banner := `
    ____                     ____            
   / ___| _ __ ___  ___ _ __|  _ \ _ __  ___ 
  | |  _ | '__/ _ \/ _ \ '_ \ |_) | '_ \/ __|
  | |_| || | |  __/  __/ | | |  __/| |_) \__ \
   \____|_|  \___|\___|_| |_|_|   | .__/|___/
        Optimize AWS for Sustainability       
`
	if colorize {
		fmt.Fprintf(w, "%s%s%s\n", ColorGreen, banner, ColorReset)
	} else {
		fmt.Fprintf(w, "%s\n", banner)
	}
}

// printSustainabilitySummary prints a summary of CO2 emissions and potential savings
func printSustainabilitySummary(w io.Writer, report []ReportItem, colorize bool) {
	// Calculate total CO2 and potential savings
	var totalCO2 float64
	var potentialCO2Savings float64
	var totalCost float64
	var potentialCostSavings float64

	// Process each report item
	for _, item := range report {
		// Extract CO2 footprint
		var itemCO2 float64

		// For RDS, look for "CO2 Footprint: X kg"
		if item.GetResourceType() == ResourceTypeRDS {
			// Try using markdown bold format first (most common in our output)
			if strings.Contains(item.Analysis, "CO2 Footprint:") {
				itemCO2 = extractNumberAfterPhrase(item.Analysis, "CO2 Footprint:")
			} else if item.GetResourceType() == ResourceTypeEC2 {
			}
			// For EC2, look for the Monthly CO2 Footprint calculation
			if strings.Contains(item.Analysis, "Monthly CO2 Footprint Calculation") {
				// Try to find the calculation result after "="
				re := regexp.MustCompile(`= ([\d\.]+) kg CO2/month`)
				matches := re.FindStringSubmatch(item.Analysis)
				if len(matches) > 1 {
					itemCO2, _ = strconv.ParseFloat(matches[1], 64)
				}
			}
		} else if item.GetResourceType() == ResourceTypeS3 {
			// For S3, try the standard format
			if strings.Contains(item.Analysis, "CO2 Footprint:") {
				itemCO2 = extractNumberAfterPhrase(item.Analysis, "CO2 Footprint:")
			}
		}

		// Extract cost
		var itemCost float64
		if strings.Contains(item.Analysis, "Estimated Monthly Cost:") {
			costText := item.Analysis[strings.Index(item.Analysis, "Estimated Monthly Cost:"):]
			if strings.Contains(costText, "$") {
				itemCost = extractNumberAfterPhrase(costText, "$")
			}
		}

		// Extract savings
		var itemCostSavings float64
		if strings.Contains(item.Analysis, "Monthly Savings Potential:") {
			savingsText := item.Analysis[strings.Index(item.Analysis, "Monthly Savings Potential:"):]
			if strings.Contains(savingsText, "$") {
				itemCostSavings = extractNumberAfterPhrase(savingsText, "$")
			}
		}

		// Extract or calculate CO2 savings
		var itemCO2Savings float64

		// Calculate CO2 savings using the same ratio as cost savings
		if itemCO2 > 0 && itemCost > 0 && itemCostSavings > 0 {
			savingsRatio := itemCostSavings / itemCost
			itemCO2Savings = itemCO2 * savingsRatio
		}

		// Add to totals
		totalCO2 += itemCO2
		totalCost += itemCost
		potentialCO2Savings += itemCO2Savings
		potentialCostSavings += itemCostSavings
	}

	// Print sustainability section header
	if colorize {
		fmt.Fprintf(w, "\n\n%s╔══════════════════════════════════════════════════════════════╗%s\n", ColorGreen, ColorReset)
		fmt.Fprintf(w, "%s║                SUSTAINABILITY IMPACT SUMMARY                  ║%s\n", ColorGreen+ColorBold, ColorReset)
		fmt.Fprintf(w, "%s╚══════════════════════════════════════════════════════════════╝%s\n", ColorGreen, ColorReset)
	} else {
		fmt.Fprintln(w, "\n\n╔══════════════════════════════════════════════════════════════╗")
		fmt.Fprintln(w, "║                SUSTAINABILITY IMPACT SUMMARY                  ║")
		fmt.Fprintln(w, "╚══════════════════════════════════════════════════════════════╝")
	}

	// Carbon metrics with fancy formatting
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	// header
	fmt.Fprintln(tw, "METRIC\tCURRENT\tPOTENTIAL\tSAVING%")
	// carbon line
	fmt.Fprintf(tw, "CO2 Emissions\t%.2f kg CO₂e\t%.2f kg CO₂e\t%.1f%%\n",
		totalCO2, potentialCO2Savings, safePercentage(potentialCO2Savings, totalCO2))
	// cost line
	fmt.Fprintf(tw, "Cost ($)\t%.2f\t%.2f\t%.1f%%\n",
		totalCost, potentialCostSavings, safePercentage(potentialCostSavings, totalCost))
	tw.Flush()

	// Environmental equivalents
	if colorize {
		fmt.Fprintf(w, "\n%sENVIRONMENTAL EQUIVALENTS%s\n", ColorBold, ColorReset)
		fmt.Fprintf(w, "─────────────────────────\n")
	} else {
		fmt.Fprintf(w, "\nENVIRONMENTAL EQUIVALENTS\n")
		fmt.Fprintf(w, "─────────────────────────\n")
	}

	// Convert CO2 savings to tree-months
	// A typical tree absorbs ~21 kg CO2 per year (1.75 kg per month)
	treesNeeded := totalCO2 / 1.75
	treesSaved := potentialCO2Savings / 1.75

	// Convert CO2 to miles driven (average car emits ~404g CO2 per mile)
	milesDriven := totalCO2 * 1000 / 404
	milesSaved := potentialCO2Savings * 1000 / 404

	// Print equivalents with color coding
	if colorize {
		fmt.Fprintf(w, "• Current emissions equivalent to: %s%.1f trees%s absorbing CO2 for one month\n",
			ColorRed, treesNeeded, ColorReset)
		fmt.Fprintf(w, "• Optimization would save the equivalent of: %s%.1f trees%s per month\n",
			ColorGreen, treesSaved, ColorReset)
		fmt.Fprintf(w, "• Current emissions equivalent to driving %s%.1f miles%s (%.1f km)\n",
			ColorRed, milesDriven, ColorReset, milesDriven*1.60934)
		fmt.Fprintf(w, "• Optimization would save the equivalent of driving %s%.1f miles%s (%.1f km)\n",
			ColorGreen, milesSaved, ColorReset, milesSaved*1.60934)
	} else {
		fmt.Fprintf(w, "• Current emissions equivalent to: %.1f trees absorbing CO2 for one month\n", treesNeeded)
		fmt.Fprintf(w, "• Optimization would save the equivalent of: %.1f trees per month\n", treesSaved)
		fmt.Fprintf(w, "• Current emissions equivalent to driving %.1f miles (%.1f km)\n",
			milesDriven, milesDriven*1.60934)
		fmt.Fprintf(w, "• Optimization would save the equivalent of driving %.1f miles (%.1f km)\n",
			milesSaved, milesSaved*1.60934)
	}

	// Annual projections
	if colorize {
		fmt.Fprintf(w, "\n%sANNUAL PROJECTIONS%s\n", ColorBold, ColorReset)
		fmt.Fprintf(w, "──────────────────\n")
	} else {
		fmt.Fprintf(w, "\nANNUAL PROJECTIONS\n")
		fmt.Fprintf(w, "──────────────────\n")
	}
	fmt.Fprintf(w, "• Annual CO2 emissions: %.2f kg CO2e\n", totalCO2*12)
	fmt.Fprintf(w, "• Potential annual CO2 reduction: %.2f kg CO2e\n", potentialCO2Savings*12)

	// Cost savings
	if colorize {
		fmt.Fprintf(w, "\n%sFINANCIAL IMPACT%s\n", ColorBold, ColorReset)
		fmt.Fprintf(w, "───────────────\n")
	} else {
		fmt.Fprintf(w, "\nFINANCIAL IMPACT\n")
		fmt.Fprintf(w, "───────────────\n")
	}
	fmt.Fprintf(w, "• Monthly cost: $%.2f\n", totalCost)
	fmt.Fprintf(w, "• Potential monthly savings: $%.2f (%.1f%%)\n",
		potentialCostSavings,
		safePercentage(potentialCostSavings, totalCost))
	fmt.Fprintf(w, "• Projected annual savings: $%.2f\n", potentialCostSavings*12)
}

// Helper function to extract numbers from text
func extractNumberAfterPhrase(text, phrase string) float64 {
	index := strings.Index(text, phrase)
	if index == -1 {
		return 0
	}

	// Get the substring after the phrase
	substring := text[index+len(phrase):]

	// Extract a number at the start of this substring
	re := regexp.MustCompile(`([\d\.]+)`)
	matches := re.FindStringSubmatch(substring)
	if len(matches) > 0 {
		if val, err := strconv.ParseFloat(matches[1], 64); err == nil {
			return val
		}
	}
	return 0
}

// safePercentage calculates a percentage safely avoiding division by zero
func safePercentage(part, whole float64) float64 {
	if whole == 0 {
		return 0
	}
	return 100 - ((part / whole) * 100)
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
	if strings.Contains(analysis, "Name:") {
		parts := strings.Split(analysis, "Name:")
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
	if strings.Contains(analysis, "Instance ID:") {
		parts := strings.Split(analysis, "Instance ID:")
		if len(parts) > 1 {
			idPart := strings.TrimSpace(parts[1])
			endPos := strings.Index(idPart, "\n")
			if endPos > 0 {
				return strings.TrimSpace(idPart[:endPos])
			}
		}
	}

	// Look for "ID: i-XXXXXXXXXX" pattern
	if strings.Contains(analysis, "ID:") {
		parts := strings.Split(analysis, "ID:")
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

func extractRDSInstanceID(analysis string) string {
	// Look for "RDS Instance Analysis: INSTANCE_ID" pattern
	if strings.Contains(analysis, "RDS Instance Analysis:") {
		parts := strings.Split(analysis, "RDS Instance Analysis:")
		if len(parts) > 1 {
			idPart := strings.TrimSpace(parts[1])
			endPos := strings.Index(idPart, "\n")
			if endPos > 0 {
				return strings.TrimSpace(idPart[:endPos])
			}
		}
	}

	// Look for "ID: DATABASE_ID" pattern
	if strings.Contains(analysis, "ID:") {
		parts := strings.Split(analysis, "ID:")
		if len(parts) > 1 {
			idPart := strings.TrimSpace(parts[1])
			endPos := strings.Index(idPart, "\n")
			if endPos > 0 {
				id := strings.TrimSpace(idPart[:endPos])
				// RDS IDs typically don't have a prefix like i- for EC2
				// This is a simple check - may need to be improved
				if !strings.HasPrefix(id, "i-") && strings.Contains(analysis, "RDS") {
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

// Print RDS details section header
func printRDSDetailsHeader(w io.Writer, colorize bool) {
	if colorize {
		fmt.Fprintf(w, "\n%sRDS INSTANCE DETAILS%s\n", ColorBold+ColorBlue, ColorReset)
		fmt.Fprintln(w, strings.Repeat("=", 19))
	} else {
		fmt.Fprintln(w, "\nRDS INSTANCE DETAILS")
		fmt.Fprintln(w, strings.Repeat("=", 19))
	}
}

// printEC2Details prints detailed analysis for an EC2 instance with coloring
func printEC2Details(w io.Writer, index int, item ReportItem, colorize bool) {
	// Section header (already colored in previous step)
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

	// --- Apply coloring to labels ---
	labelColor := ""
	reset := ""
	bold := ""
	if colorize {
		labelColor = ColorCyan
		reset = ColorReset
		bold = ColorBold
	}

	// Instance metadata
	if !item.Instance.LaunchTime.IsZero() {
		fmt.Fprintf(w, "%sLaunch Time:%s %s\n", labelColor, reset, item.Instance.LaunchTime.Format(time.RFC3339))
	}
	fmt.Fprintf(w, "%sCPU Utilization (7-day avg):%s %.1f%%\n", labelColor, reset, item.Instance.CPUAvg7d)

	// Tags
	if len(item.Instance.Tags) > 0 {
		fmt.Fprintf(w, "%sTags:%s\n", bold+labelColor, reset) // Bold and color the label
		// Sort tags for consistent output
		keys := make([]string, 0, len(item.Instance.Tags))
		for k := range item.Instance.Tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "  %s%s:%s %s\n", labelColor, k, reset, item.Instance.Tags[k]) // Color the key
		}
	}

	// Analysis
	fmt.Fprintf(w, "\n%sAI ANALYSIS:%s\n", bold+labelColor, reset) // Bold and color the label
	fmt.Fprintln(w, item.Analysis)                                 // Print analysis content as is
}

// printS3Details prints detailed analysis for an S3 bucket with coloring
func printS3Details(w io.Writer, index int, item ReportItem, colorize bool) {
	// Section header (already colored)
	title := fmt.Sprintf("Bucket %d: %s", index, item.S3Bucket.BucketName)
	if colorize {
		fmt.Fprintf(w, "\n%s%s%s\n", ColorBold+ColorBlue, title, ColorReset)
		fmt.Fprintln(w, strings.Repeat("-", len(title)))
	} else {
		fmt.Fprintf(w, "\n%s\n", title)
		fmt.Fprintln(w, strings.Repeat("-", len(title)))
	}

	// --- Apply coloring to labels ---
	labelColor := ""
	reset := ""
	bold := ""
	if colorize {
		labelColor = ColorCyan
		reset = ColorReset
		bold = ColorBold
	}

	// Bucket metadata
	if item.S3Bucket.Region != "" {
		fmt.Fprintf(w, "%sRegion:%s %s\n", labelColor, reset, item.S3Bucket.Region)
	}
	if !item.S3Bucket.CreationDate.IsZero() {
		fmt.Fprintf(w, "%sCreation Date:%s %s\n", labelColor, reset, item.S3Bucket.CreationDate.Format(time.RFC3339))
	}
	fmt.Fprintf(w, "%sSize:%s %.2f GB\n", labelColor, reset, float64(item.S3Bucket.SizeBytes)/(1024*1024*1024))
	fmt.Fprintf(w, "%sObject Count:%s %d\n", labelColor, reset, item.S3Bucket.ObjectCount)
	if !item.S3Bucket.LastModified.IsZero() {
		fmt.Fprintf(w, "%sLast Modified:%s %s\n", labelColor, reset, item.S3Bucket.LastModified.Format(time.RFC3339))
	}

	// Storage class breakdown
	if len(item.S3Bucket.StorageClasses) > 0 {
		fmt.Fprintf(w, "\n%sStorage Classes:%s\n", bold+labelColor, reset) // Bold and color label
		// Sort classes for consistent output
		classes := make([]string, 0, len(item.S3Bucket.StorageClasses))
		for c := range item.S3Bucket.StorageClasses {
			classes = append(classes, c)
		}
		sort.Strings(classes)
		for _, class := range classes {
			size := item.S3Bucket.StorageClasses[class]
			percentage := 0.0
			if item.S3Bucket.SizeBytes > 0 {
				percentage = float64(size) / float64(item.S3Bucket.SizeBytes) * 100
			}
			fmt.Fprintf(w, "  %s%s:%s %.2f GB (%.1f%%)\n",
				labelColor, class, reset, // Color the class name
				float64(size)/(1024*1024*1024), percentage)
		}
	}

	// Access patterns
	if len(item.S3Bucket.AccessFrequency) > 0 {
		fmt.Fprintf(w, "\n%sAccess Patterns (daily average):%s\n", bold+labelColor, reset) // Bold and color label
		// Sort operations for consistent output
		ops := make([]string, 0, len(item.S3Bucket.AccessFrequency))
		for op := range item.S3Bucket.AccessFrequency {
			ops = append(ops, op)
		}
		sort.Strings(ops)
		for _, op := range ops {
			count := item.S3Bucket.AccessFrequency[op]
			fmt.Fprintf(w, "  %s%s:%s %.1f\n", labelColor, op, reset, count) // Color the operation name
		}
	}

	// Lifecycle rules
	fmt.Fprintf(w, "\n%sLifecycle Rules:%s ", bold+labelColor, reset) // Bold and color label (note the space at the end)
	if len(item.S3Bucket.LifecycleRules) > 0 {
		fmt.Fprintln(w) // Newline after the label if rules exist
		// Sort rules by ID for consistent output
		rules := item.S3Bucket.LifecycleRules
		sort.Slice(rules, func(i, j int) bool {
			return rules[i].ID < rules[j].ID
		})
		for _, rule := range rules {
			ruleStatus := "Disabled"
			statusColor := ColorYellow // Default to yellow for disabled
			if rule.Status == "Enabled" {
				ruleStatus = "Enabled"
				statusColor = ColorGreen
			}
			if !colorize {
				statusColor = ""
			} // Clear color if not colorizing

			fmt.Fprintf(w, "  %sRule '%s'%s (%s%s%s): ", labelColor, rule.ID, reset, statusColor, ruleStatus, reset)

			if rule.HasTransitions {
				fmt.Fprintf(w, "Transitions at %d days", rule.ObjectAgeThreshold)
			} else {
				fmt.Fprintf(w, "No transitions")
			}
			if rule.HasExpirations {
				// Add comma only if transitions were mentioned
				if rule.HasTransitions {
					fmt.Fprint(w, ",")
				}
				fmt.Fprintf(w, " Expires at %d days", rule.ObjectAgeThreshold)
			}
			fmt.Fprintln(w)
		}
	} else {
		fmt.Fprintln(w, "None configured") // Print on the same line as the label if none
	}

	// Tags
	if len(item.S3Bucket.Tags) > 0 {
		fmt.Fprintf(w, "\n%sTags:%s\n", bold+labelColor, reset) // Bold and color the label
		// Sort tags for consistent output
		keys := make([]string, 0, len(item.S3Bucket.Tags))
		for k := range item.S3Bucket.Tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "  %s%s:%s %s\n", labelColor, k, reset, item.S3Bucket.Tags[k]) // Color the key
		}
	}

	// Analysis
	fmt.Fprintf(w, "\n%sAI ANALYSIS:%s\n", bold+labelColor, reset) // Bold and color the label
	fmt.Fprintln(w, item.Analysis)                                 // Print analysis content as is
}

// printRDSDetails prints detailed analysis for an RDS instance with coloring
func printRDSDetails(w io.Writer, index int, item ReportItem, colorize bool) {
	// Section header (already colored)
	title := fmt.Sprintf("RDS Instance %d: %s (%s)", index, item.RDSInstance.InstanceID, item.RDSInstance.InstanceType)
	if colorize {
		fmt.Fprintf(w, "\n%s%s%s\n", ColorBold+ColorBlue, title, ColorReset)
		fmt.Fprintln(w, strings.Repeat("-", len(title)))
	} else {
		fmt.Fprintf(w, "\n%s\n", title)
		fmt.Fprintln(w, strings.Repeat("-", len(title)))
	}

	// --- Apply coloring to labels ---
	labelColor := ""
	reset := ""
	bold := ""
	if colorize {
		labelColor = ColorCyan
		reset = ColorReset
		bold = ColorBold
	}

	// Instance metadata
	fmt.Fprintf(w, "%sEngine:%s %s %s\n", labelColor, reset, item.RDSInstance.Engine, item.RDSInstance.EngineVersion)
	fmt.Fprintf(w, "%sStorage:%s %d GB (%s)\n", labelColor, reset, item.RDSInstance.AllocatedStorage, item.RDSInstance.StorageType)
	fmt.Fprintf(w, "%sMulti-AZ:%s %t\n", labelColor, reset, item.RDSInstance.MultiAZ)
	if !item.RDSInstance.LaunchTime.IsZero() {
		fmt.Fprintf(w, "%sLaunch Time:%s %s\n", labelColor, reset, item.RDSInstance.LaunchTime.Format(time.RFC3339))
	}
	fmt.Fprintf(w, "%sCPU Utilization (7-day avg):%s %.1f%%\n", labelColor, reset, item.RDSInstance.CPUAvg7d)
	fmt.Fprintf(w, "%sStorage Used:%s %.1f%%\n", labelColor, reset, item.RDSInstance.StorageUsed)
	fmt.Fprintf(w, "%sConnections (7-day avg):%s %.1f\n", labelColor, reset, item.RDSInstance.ConnectionsAvg7d)
	fmt.Fprintf(w, "%sIOPS (7-day avg):%s %.1f\n", labelColor, reset, item.RDSInstance.IOPSAvg7d)

	// Tags
	if len(item.RDSInstance.Tags) > 0 {
		fmt.Fprintf(w, "%sTags:%s\n", bold+labelColor, reset) // Bold and color the label
		// Sort tags for consistent output
		keys := make([]string, 0, len(item.RDSInstance.Tags))
		for k := range item.RDSInstance.Tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(w, "  %s%s:%s %s\n", labelColor, k, reset, item.RDSInstance.Tags[k]) // Color the key
		}
	}

	// Analysis
	fmt.Fprintf(w, "\n%sAI ANALYSIS:%s\n", bold+labelColor, reset) // Bold and color the label
	fmt.Fprintln(w, item.Analysis)                                 // Print analysis content as is
}

// // getEfficiencyStatus returns a status based on CPU utilization
// func getEfficiencyStatus(cpuAvg float64) string {
// 	if cpuAvg < 5 {
// 		return "CRITICAL"
// 	} else if cpuAvg < 20 {
// 		return "WARNING"
// 	} else {
// 		return "GOOD"
// 	}
// }

// // getRDSEfficiencyStatus returns a status based on CPU utilization
// func getRDSEfficiencyStatus(cpuAvg float64) string {
// 	if cpuAvg < 5 {
// 		return "CRITICAL"
// 	} else if cpuAvg < 20 {
// 		return "WARNING"
// 	} else {
// 		return "GOOD"
// 	}
// }
