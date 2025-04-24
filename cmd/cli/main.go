package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/briandowns/spinner"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"

	pkg "github.com/alexalbu001/greenops/pkg"
)

// Command-line flags
var (
	apiURL       string
	region       string
	profile      string
	outputFile   string
	debug        bool
	timeout      int
	resourceCap  int
	noColor      bool
	configFile   string
	generateConf bool
	asyncMode    bool
	pollInterval int
	maxPollRetry int
	resources    string
	pdfOutput    string
	verbose      bool
)

// ServerResponse represents the API response format
type ServerResponse struct {
	Report []pkg.ReportItem `json:"report"`
}

func init() {
	// Define command-line flags
	flag.StringVar(&configFile, "config", "", "Path to configuration file")
	flag.BoolVar(&generateConf, "init", false, "Generate a default configuration file")
	flag.StringVar(&apiURL, "api", "https://8tse26l4fi.execute-api.eu-west-1.amazonaws.com/analyze", "GreenOps API URL")
	flag.StringVar(&region, "region", "", "AWS Region (defaults to AWS_REGION env var or config file)")
	flag.StringVar(&profile, "profile", "", "AWS Profile (defaults to AWS_PROFILE env var or default profile)")
	flag.StringVar(&outputFile, "output", "", "Save results to file (default outputs to stdout)")
	flag.BoolVar(&debug, "debug", false, "Enable debug logging")
	flag.IntVar(&timeout, "timeout", 60, "API request timeout in seconds")
	flag.IntVar(&resourceCap, "limit", 10, "Maximum number of resources to scan")
	flag.BoolVar(&noColor, "no-color", false, "Disable colorized output")
	flag.BoolVar(&asyncMode, "async", true, "Use asynchronous processing mode")
	flag.IntVar(&pollInterval, "poll-interval", 5, "Polling interval in seconds for async mode")
	flag.IntVar(&maxPollRetry, "poll-max", 60, "Maximum number of polling attempts")
	flag.StringVar(&resources, "resources", "ec2,s3,rds", "Comma-separated list of resources to scan (ec2,s3,rds)")
	flag.BoolVar(&verbose, "verbose", false, "Show debug and scan logs (stderr)")
}

// isTerminal detects if the output is going to a terminal
func isTerminal(f *os.File) bool {
	fileInfo, err := f.Stat()
	if err != nil {
		return false
	}
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

// printUsageInfo prints detailed usage information
func printUsageInfo() {
	fmt.Printf(`GreenOps CLI
A tool for optimizing AWS resource usage and reducing carbon footprint.

Basic Usage:
  greenops [options]

Operating Modes:
  - Synchronous (default): Directly analyze resources and wait for results
  - Asynchronous (--async): Submit jobs for background processing

Examples:
  greenops --limit 10                     # Analyze up to 10 EC2 instances synchronously
  greenops --async --limit 50             # Analyze up to 50 EC2 instances asynchronously
  greenops --output results.json          # Save results to a file
  greenops --region eu-west-1             # Specify AWS region
  greenops --profile prod                 # Use specific AWS profile
  greenops --debug                        # Enable debug logging

`)
	flag.PrintDefaults()
}

// pollForJobResults polls the API for job results until completed or max attempts reached
func pollForJobResults(ctx context.Context, jobID string, cfg *pkg.Config, client *http.Client) ([]pkg.ReportItem, error) {
	// Construct URLs
	baseURL := cfg.API.URL
	if strings.HasSuffix(baseURL, "/analyze") {
		baseURL = baseURL[:len(baseURL)-len("/analyze")]
	}
	jobURL := fmt.Sprintf("%s/jobs/%s", baseURL, jobID)
	resultsURL := fmt.Sprintf("%s/jobs/%s/results", baseURL, jobID)

	// Start spinner on stderr
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond, spinner.WithWriter(os.Stderr))
	s.Prefix = "⠋ Waiting for analysis… "
	s.Start()

	var lastCompleted int
	var noProgress int

	for attempt := 0; attempt < maxPollRetry; attempt++ {
		// Update spinner
		// s.Suffix = fmt.Sprintf("", lastCompleted, cfg.Scan.Limit)

		// Fetch status
		req, err := http.NewRequestWithContext(ctx, "GET", jobURL, nil)
		if err != nil {
			s.Stop()
			return nil, fmt.Errorf("failed to create job status request: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			s.Stop()
			return nil, fmt.Errorf("failed to get job status: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var st struct {
			Status         string `json:"status"`
			TotalItems     int    `json:"total_items"`
			CompletedItems int    `json:"completed_items"`
			FailedItems    int    `json:"failed_items"`
		}
		if err := json.Unmarshal(body, &st); err != nil {
			// transient parse error; retry
			time.Sleep(time.Duration(pollInterval) * time.Second)
			continue
		}

		// Progress tracking
		if st.CompletedItems > lastCompleted {
			lastCompleted = st.CompletedItems
			noProgress = 0
		} else {
			noProgress++
		}

		// Done?
		if st.Status == "completed" || st.Status == "failed" ||
			(st.CompletedItems+st.FailedItems >= st.TotalItems && noProgress >= 3) {
			break
		}

		time.Sleep(time.Duration(pollInterval) * time.Second)
	}

	// Stop spinner and fetch results
	s.Stop()
	// fmt.Fprintln(os.Stderr, "Getting results directly from", resultsURL)
	return getResultsDirectly(ctx, resultsURL, client)
}

// getResultsDirectly retrieves results from the direct results endpoint
func getResultsDirectly(ctx context.Context, resultsURL string, client *http.Client) ([]pkg.ReportItem, error) {
	log.Printf("Getting results directly from %s", resultsURL)

	req, err := http.NewRequestWithContext(ctx, "GET", resultsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create results request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get results: %v", err)
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to read results response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("results API returned error status %d: %s", resp.StatusCode, body)
	}

	// Parse the response
	var resultsResp struct {
		Results []pkg.ReportItem `json:"results"`
	}

	err = json.Unmarshal(body, &resultsResp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse results: %v", err)
	}

	log.Printf("Successfully retrieved %d report items directly", len(resultsResp.Results))
	return resultsResp.Results, nil
}

func main() {
	// Parse command-line flags
	flag.Parse()
	if !verbose {
		// send the default logger to stderr only
		log.SetOutput(os.Stderr)
	} else {
		// include file/line info for debug
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	}

	// Show help only if explicitly requested with -h or --help
	if len(os.Args) == 2 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		printUsageInfo()
		return
	}
	// Setup logger
	if debug {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	} else {
		log.SetFlags(0)
	}

	// Handle configuration
	if generateConf {
		// Get default configuration
		defaultConfig := &pkg.Config{}
		defaultConfig.API.URL = "https://8tse26l4fi.execute-api.eu-west-1.amazonaws.com/analyze"
		defaultConfig.API.Timeout = 60
		defaultConfig.Scan.Limit = 10
		defaultConfig.Scan.Resources = []string{"ec2", "s3"}
		defaultConfig.Scan.Metrics.PeriodDays = 7
		defaultConfig.Output.Colors = true
		defaultConfig.Output.Format = "text"
		defaultConfig.Output.Verbosity = "normal"

		// Determine output path
		outputPath := configFile
		if outputPath == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				log.Fatalf("Failed to get user home directory: %v", err)
			}
			outputPath = filepath.Join(home, ".greenops", "config.json")
		}

		// Create directory if needed
		configDir := filepath.Dir(outputPath)
		if err := os.MkdirAll(configDir, 0755); err != nil {
			log.Fatalf("Failed to create config directory: %v", err)
		}

		// Marshal config to JSON
		data, err := json.MarshalIndent(defaultConfig, "", "  ")
		if err != nil {
			log.Fatalf("Failed to generate config: %v", err)
		}

		// Write to file
		if err := os.WriteFile(outputPath, data, 0644); err != nil {
			log.Fatalf("Failed to write config file: %v", err)
		}

		fmt.Printf("Configuration file generated at: %s\n", outputPath)
		return
	}

	// Load or create configuration
	var cfg *pkg.Config

	// If config file is specified, try to load it
	if configFile != "" {
		if data, err := os.ReadFile(configFile); err == nil {
			cfg = &pkg.Config{}
			if err := json.Unmarshal(data, cfg); err != nil {
				log.Fatalf("Failed to parse config file: %v", err)
			}
		} else {
			log.Fatalf("Failed to read config file: %v", err)
		}
	} else {
		// Use default configuration
		cfg = &pkg.Config{}
		cfg.API.URL = apiURL
		cfg.API.Timeout = timeout
		cfg.AWS.Region = region
		cfg.AWS.Profile = profile
		cfg.Scan.Limit = resourceCap
		cfg.Scan.Resources = []string{"ec2", "s3", "rds"}
		cfg.Scan.Resources = strings.Split(resources, ",")
		cfg.Scan.Metrics.PeriodDays = 7
		cfg.Output.Colors = !noColor
		cfg.Output.Format = "text"
		cfg.Output.Verbosity = "normal"
	}

	// Override config with command line arguments if provided
	if apiURL != "" {
		cfg.API.URL = apiURL
	}
	if region != "" {
		cfg.AWS.Region = region
	}
	if profile != "" {
		cfg.AWS.Profile = profile
	}
	if timeout > 0 {
		cfg.API.Timeout = timeout
	}
	if resourceCap > 0 {
		cfg.Scan.Limit = resourceCap
	}
	if noColor {
		cfg.Output.Colors = false
	}

	// Set up AWS context
	ctx := context.Background()
	var awsConfigOpts []func(*awsconfig.LoadOptions) error

	if cfg.AWS.Region != "" {
		awsConfigOpts = append(awsConfigOpts, awsconfig.WithRegion(cfg.AWS.Region))
	}
	if cfg.AWS.Profile != "" {
		awsConfigOpts = append(awsConfigOpts, awsconfig.WithSharedConfigProfile(cfg.AWS.Profile))
	}

	log.Println("Loading AWS configuration...")
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsConfigOpts...)
	if err != nil {
		log.Fatalf("Failed to load AWS configuration: %v", err)
	}

	// Scan resources
	scanResults, err := pkg.ScanResources(ctx, awsCfg, cfg.Scan.Resources, cfg.Scan.Limit, cfg.Scan.Metrics.PeriodDays)
	if err != nil {
		log.Fatalf("Failed to scan resources: %v", err)
	}

	// Initialize request payload
	requestPayload := map[string]interface{}{}
	totalResourceCount := 0

	// Process EC2 instances
	if instances, ok := scanResults["ec2"].([]pkg.Instance); ok && len(instances) > 0 {
		log.Printf("Found %d EC2 instances for analysis", len(instances))
		requestPayload["instances"] = instances
		totalResourceCount += len(instances)
	}

	// Process S3 buckets
	if buckets, ok := scanResults["s3"].([]pkg.S3Bucket); ok && len(buckets) > 0 {
		log.Printf("Found %d S3 buckets for analysis", len(buckets))
		requestPayload["s3_buckets"] = buckets
		totalResourceCount += len(buckets)
	}

	// Process RDS instances
	if rdsInstances, ok := scanResults["rds"].([]pkg.RDSInstance); ok && len(rdsInstances) > 0 {
		log.Printf("Found %d RDS instances for analysis", len(rdsInstances))
		requestPayload["rds_instances"] = rdsInstances
		totalResourceCount += len(rdsInstances)
	}

	if totalResourceCount == 0 {
		log.Println("No resources found to analyze.")
		return
	}

	// Prepare request payload
	requestBody, err := json.Marshal(requestPayload)
	if err != nil {
		log.Fatalf("Failed to marshal request: %v", err)
	}

	// Create HTTP client
	client := &http.Client{
		Timeout: time.Duration(cfg.API.Timeout) * time.Second,
	}

	// Process based on mode (sync or async)
	if asyncMode {
		// log.Printf("Using asynchronous mode for processing %d resources...", totalResourceCount)

		// Send async request
		req, err := http.NewRequestWithContext(ctx, "POST", cfg.API.URL, bytes.NewBuffer(requestBody))
		if err != nil {
			log.Fatalf("Failed to create HTTP request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")

		// Send request
		resp, err := client.Do(req)
		if err != nil {
			log.Fatalf("Failed to send request: %v", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Fatalf("Failed to read response: %v", err)
		}

		if resp.StatusCode != http.StatusAccepted {
			log.Fatalf("API returned error status %d: %s", resp.StatusCode, body)
		}

		// Parse job ID from response
		var jobResponse struct {
			JobID      string `json:"job_id"`
			Status     string `json:"status"`
			TotalItems int    `json:"total_items"`
		}

		err = json.Unmarshal(body, &jobResponse)
		if err != nil {
			log.Fatalf("Failed to parse job response: %v", err)
		}

		log.Printf("Job submitted: ID=%s, Status=%s, Items=%d",
			jobResponse.JobID, jobResponse.Status, jobResponse.TotalItems)

		// Poll for results
		report, err := pollForJobResults(ctx, jobResponse.JobID, cfg, client)
		if err != nil {
			log.Fatalf("Failed to get job results: %v", err)
		}

		// Display results
		if outputFile != "" {
			// Write to file
			file, err := os.Create(outputFile)
			if err != nil {
				log.Fatalf("Failed to create output file: %v", err)
			}
			defer file.Close()

			// Use our formatter for better output
			pkg.FormatAnalysisReport(file, report, false) // No colors in file output
			log.Printf("Results saved to %s", outputFile)
		} else {
			// Use colors if stdout is a terminal and colors are enabled
			useColors := isTerminal(os.Stdout) && cfg.Output.Colors

			// Print to console using our formatter
			pkg.FormatAnalysisReport(os.Stdout, report, useColors)
		}
	} else {
		// Synchronous mode
		log.Printf("Sending %d resources to GreenOps API for analysis with timeout of %d seconds...",
			totalResourceCount, cfg.API.Timeout)
		httpCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.API.Timeout)*time.Second)
		defer cancel()

		// Create HTTP request with timeout
		req, err := http.NewRequestWithContext(httpCtx, "POST", cfg.API.URL, bytes.NewBuffer(requestBody))
		if err != nil {
			log.Fatalf("Failed to create HTTP request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")

		// Add retry logic for HTTP requests
		maxRetries := 3
		var resp *http.Response

		for attempt := 0; attempt < maxRetries; attempt++ {
			if attempt > 0 {
				log.Printf("Retry attempt %d/%d after waiting %d seconds", attempt+1, maxRetries, attempt*5)
				time.Sleep(time.Duration(attempt*5) * time.Second) // Exponential backoff
			}

			resp, err = client.Do(req)
			if err == nil {
				break // Success, exit retry loop
			}

			if attempt == maxRetries-1 || (!strings.Contains(err.Error(), "timeout") &&
				!strings.Contains(err.Error(), "deadline exceeded")) {
				// Last attempt or non-timeout error
				if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline exceeded") {
					log.Fatalf("API request timed out after %d retries. Try increasing the timeout with --timeout or reduce the number of resources with --limit", maxRetries)
				}
				log.Fatalf("API request failed after %d retries: %v", attempt+1, err)
			}

			log.Printf("Request attempt %d failed: %v. Retrying...", attempt+1, err)
		}

		defer resp.Body.Close()

		// Read the response
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Fatalf("Failed to read API response: %v", err)
		}

		// Check response status
		if resp.StatusCode != http.StatusOK {
			if resp.StatusCode == http.StatusServiceUnavailable {
				log.Fatalf("API service unavailable (503). The service might be experiencing high load or temporary issues with the underlying models. Try again later or with fewer resources.")
			} else {
				log.Fatalf("API returned error status %d: %s", resp.StatusCode, respBody)
			}
		}

		// Parse the response
		var apiResponse ServerResponse
		if err := json.Unmarshal(respBody, &apiResponse); err != nil {
			log.Fatalf("Failed to parse API response: %v", err)
		}

		// Output the analysis results
		if outputFile != "" {
			// Write to file
			file, err := os.Create(outputFile)
			if err != nil {
				log.Fatalf("Failed to create output file: %v", err)
			}
			defer file.Close()

			pkg.FormatAnalysisReport(file, apiResponse.Report, false) // No colors in file output
			log.Printf("Results saved to %s", outputFile)
		} else {
			// Use colors if stdout is a terminal and colors are enabled
			useColors := isTerminal(os.Stdout) && cfg.Output.Colors

			// Print to console using our formatter
			pkg.FormatAnalysisReport(os.Stdout, apiResponse.Report, useColors)
		}
	}
}
