package pkg

import (
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

// FormatAnalysisReport prints the analysis results in a user-friendly format
func FormatAnalysisReport(w io.Writer, report []ReportItem, colorize bool) {
	// Header
	printHeader(w, "GreenOps Analysis Report", colorize)
	fmt.Fprintf(w, "Generated: %s\n", time.Now().Format(time.RFC1123))
	fmt.Fprintf(w, "Instances analyzed: %d\n\n", len(report))

	// Summary table
	printSummaryTable(w, report, colorize)

	// Detailed analysis for each instance
	for i, item := range report {
		printInstanceAnalysis(w, i+1, item, colorize)
	}
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

func printSummaryTable(w io.Writer, report []ReportItem, colorize bool) {
	// Print table header
	headers := []string{"INSTANCE ID", "TYPE", "CPU AVG", "STATUS"}
	fmt.Fprintf(w, "%-15s %-10s %-10s %-15s\n", headers[0], headers[1], headers[2], headers[3])
	fmt.Fprintln(w, strings.Repeat("-", 55))

	// Print table rows
	for _, item := range report {
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
