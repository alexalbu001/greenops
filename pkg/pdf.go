package pkg

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jung-kurt/gofpdf"
)

// ExportReportToPDF exports the analysis report to a PDF file
func ExportReportToPDF(report []ReportItem, outputPath string) error {
	// Create a new PDF with A4 portrait, mm units, 72 DPI
	pdf := gofpdf.New("P", "mm", "A4", "")

	// Add a page
	pdf.AddPage()

	// Set up fonts
	pdf.SetFont("Arial", "B", 16)

	// Title
	pdf.Cell(40, 10, "GreenOps Analysis Report")
	pdf.Ln(12)

	// Add date
	pdf.SetFont("Arial", "I", 10)
	pdf.Cell(40, 10, fmt.Sprintf("Generated: %s", time.Now().Format(time.RFC1123)))
	pdf.Ln(15)

	// Resource summary
	ec2Count := 0
	s3Count := 0
	rdsCount := 0

	for _, item := range report {
		switch item.GetResourceType() {
		case ResourceTypeEC2:
			ec2Count++
		case ResourceTypeS3:
			s3Count++
		case ResourceTypeRDS:
			rdsCount++
		}
	}

	// Add summary section
	pdf.SetFont("Arial", "B", 14)
	pdf.Cell(40, 10, "Resource Summary")
	pdf.Ln(10)

	pdf.SetFont("Arial", "", 12)
	pdf.Cell(40, 8, fmt.Sprintf("EC2 instances analyzed: %d", ec2Count))
	pdf.Ln(8)
	pdf.Cell(40, 8, fmt.Sprintf("S3 buckets analyzed: %d", s3Count))
	pdf.Ln(8)
	pdf.Cell(40, 8, fmt.Sprintf("RDS instances analyzed: %d", rdsCount))
	pdf.Ln(8)
	pdf.Cell(40, 8, fmt.Sprintf("Total resources analyzed: %d", ec2Count+s3Count+rdsCount))
	pdf.Ln(15)

	// Add sustainability section
	pdf.SetFont("Arial", "B", 14)
	pdf.Cell(40, 10, "Sustainability Impact")
	pdf.Ln(10)

	// Calculate CO2 and cost metrics
	var totalCO2 float64
	var potentialCO2Savings float64
	var totalCost float64
	var potentialCostSavings float64

	for _, item := range report {
		co2, cost, savings := extractResourceMetrics(item.Analysis)
		totalCO2 += co2
		totalCost += cost
		potentialCostSavings += savings
		// Estimate CO2 savings based on cost ratio
		if cost > 0 && savings > 0 {
			potentialCO2Savings += co2 * (savings / cost)
		}
	}

	// Display sustainability metrics
	pdf.SetFont("Arial", "", 12)
	pdf.Cell(40, 8, fmt.Sprintf("Monthly CO2 Emissions: %.2f kg CO2e", totalCO2))
	pdf.Ln(8)
	pdf.Cell(40, 8, fmt.Sprintf("Potential CO2 Reduction: %.2f kg CO2e (%.1f%%)",
		potentialCO2Savings, safePercentage(potentialCO2Savings, totalCO2)))
	pdf.Ln(8)
	pdf.Cell(40, 8, fmt.Sprintf("Monthly Cost: $%.2f", totalCost))
	pdf.Ln(8)
	pdf.Cell(40, 8, fmt.Sprintf("Potential Monthly Savings: $%.2f (%.1f%%)",
		potentialCostSavings, safePercentage(potentialCostSavings, totalCost)))
	pdf.Ln(15)

	// Add detailed findings for each resource
	pdf.SetFont("Arial", "B", 14)
	pdf.Cell(40, 10, "Resource Details")
	pdf.Ln(12)

	// Display each resource's findings
	for i, item := range report {
		// Add page break if needed
		if pdf.GetY() > 250 {
			pdf.AddPage()
		}

		// Resource header
		pdf.SetFont("Arial", "B", 12)

		resourceName := getResourceName(item)
		pdf.Cell(40, 8, fmt.Sprintf("%d. %s", i+1, resourceName))
		pdf.Ln(10)

		// Resource type-specific details
		pdf.SetFont("Arial", "", 10)

		switch item.GetResourceType() {
		case ResourceTypeEC2:
			pdf.Cell(40, 6, fmt.Sprintf("Type: %s", item.Instance.InstanceType))
			pdf.Ln(6)
			pdf.Cell(40, 6, fmt.Sprintf("CPU Utilization: %.1f%%", item.Instance.CPUAvg7d))
			pdf.Ln(10)
		case ResourceTypeS3:
			pdf.Cell(40, 6, fmt.Sprintf("Size: %.2f GB", float64(item.S3Bucket.SizeBytes)/(1024*1024*1024)))
			pdf.Ln(6)
			pdf.Cell(40, 6, fmt.Sprintf("Objects: %d", item.S3Bucket.ObjectCount))
			pdf.Ln(10)
		case ResourceTypeRDS:
			pdf.Cell(40, 6, fmt.Sprintf("Type: %s", item.RDSInstance.InstanceType))
			pdf.Ln(6)
			pdf.Cell(40, 6, fmt.Sprintf("CPU Utilization: %.1f%%", item.RDSInstance.CPUAvg7d))
			pdf.Ln(10)
		}

		// Extract key findings from analysis
		findings := extractKeyFindings(item.Analysis)

		pdf.SetFont("Arial", "BI", 10)
		pdf.Cell(40, 8, "Key Findings:")
		pdf.Ln(8)

		pdf.SetFont("Arial", "", 9)
		for _, finding := range findings {
			multiLineText(pdf, finding, 180)
			pdf.Ln(6)
		}

		pdf.Ln(10)
	}

	// Save the PDF
	return pdf.OutputFileAndClose(outputPath)
}

// Helper functions

// multiLineText adds text with automatic line breaks
func multiLineText(pdf *gofpdf.Fpdf, text string, width float64) {
	pdf.MultiCell(width, 5, text, "", "", false)
}

// getResourceName returns a display name for a resource
func getResourceName(item ReportItem) string {
	switch item.GetResourceType() {
	case ResourceTypeEC2:
		return fmt.Sprintf("EC2 Instance: %s", item.Instance.InstanceID)
	case ResourceTypeS3:
		return fmt.Sprintf("S3 Bucket: %s", item.S3Bucket.BucketName)
	case ResourceTypeRDS:
		return fmt.Sprintf("RDS Instance: %s", item.RDSInstance.InstanceID)
	default:
		return "Unknown Resource"
	}
}

// extractKeyFindings extracts the main findings from analysis text
func extractKeyFindings(analysis string) []string {
	var findings []string

	// Look for lines with "Finding" markers
	lines := strings.Split(analysis, "\n")
	for _, line := range lines {
		if strings.Contains(line, "**Finding**:") ||
			strings.Contains(line, "Finding:") {
			// Clean up the finding text
			finding := strings.ReplaceAll(line, "**Finding**:", "")
			finding = strings.ReplaceAll(finding, "Finding:", "")
			finding = strings.TrimSpace(finding)

			if finding != "" {
				findings = append(findings, finding)
			}
		}
	}

	// If we didn't find explicit findings, extract recommendations
	if len(findings) == 0 {
		inRecommendations := false
		for _, line := range lines {
			if strings.Contains(line, "## Recommendations") ||
				strings.Contains(line, "Recommendations:") {
				inRecommendations = true
				continue
			}

			if inRecommendations && strings.TrimSpace(line) != "" &&
				!strings.HasPrefix(line, "#") {
				findings = append(findings, strings.TrimSpace(line))

				// Only grab a few recommendations
				if len(findings) >= 3 {
					break
				}
			}
		}
	}

	return findings
}

// extractResourceMetrics extracts CO2, cost, and savings values from analysis text
func extractResourceMetrics(analysis string) (co2 float64, cost float64, costSavings float64) {
	// Look for the standard Cost & Environmental Impact section
	sectionStart := strings.Index(analysis, "## Cost & Environmental Impact")

	// If not found, try alternate section names
	if sectionStart == -1 {
		sectionStart = strings.Index(analysis, "## 3. Cost & Environmental Impact")
	}
	if sectionStart == -1 {
		sectionStart = strings.Index(analysis, "## 4. Cost & Environmental Impact")
	}

	// If we found the section, extract metrics
	if sectionStart != -1 {
		// Get the section text up to the next heading
		sectionEnd := strings.Index(analysis[sectionStart+10:], "##")
		var sectionText string
		if sectionEnd != -1 {
			sectionText = analysis[sectionStart : sectionStart+10+sectionEnd]
		} else {
			sectionText = analysis[sectionStart:]
		}

		// Extract CO2 Footprint
		co2 = extractMetricValue(sectionText, "**CO2 Footprint**:", "kg")

		// Extract Cost
		cost = extractMetricValue(sectionText, "**Estimated Monthly Cost**:", "$")

		// Extract Cost Savings
		costSavings = extractMetricValue(sectionText, "**Monthly Savings Potential**:", "$")

		return co2, cost, costSavings
	}

	// If standard section wasn't found, try alternative extraction methods

	// For CO2
	// Try finding CO2 value with regex
	re := regexp.MustCompile(`(\d+\.\d+)\s*kg CO2 per month`)
	if matches := re.FindStringSubmatch(analysis); len(matches) > 1 {
		co2, _ = strconv.ParseFloat(matches[1], 64)
	}

	// For cost and savings, if not using standard format, use rough estimates
	// based on instance/resource type
	if co2 > 0 && cost == 0 {
		// Estimate cost based on CO2 (very rough)
		// Assuming $25 per kg CO2 as a rule of thumb
		cost = co2 * 25.0

		// Estimate savings based on common patterns in the analysis
		if strings.Contains(strings.ToLower(analysis), "underutilized") ||
			strings.Contains(strings.ToLower(analysis), "over-provisioned") {
			// Significant savings potential
			costSavings = cost * 0.5
		} else {
			// Modest savings potential
			costSavings = cost * 0.25
		}
	}

	return co2, cost, costSavings
}

// extractMetricValue extracts a numeric value after a specific label
func extractMetricValue(text, label, prefix string) float64 {
	idx := strings.Index(text, label)
	if idx == -1 {
		return 0
	}

	// Get text after the label
	valueText := text[idx+len(label):]

	// Skip prefix if specified
	if prefix != "" && strings.Contains(valueText, prefix) {
		valueText = valueText[strings.Index(valueText, prefix)+len(prefix):]
	}

	// Extract the first number
	re := regexp.MustCompile(`(\d+\.\d+)`)
	matches := re.FindStringSubmatch(valueText)
	if len(matches) > 1 {
		value, err := strconv.ParseFloat(matches[1], 64)
		if err == nil {
			return value
		}
	}

	return 0
}
