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

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

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
	flag.BoolVar(&asyncMode, "async", false, "Use asynchronous processing mode")
	flag.IntVar(&pollInterval, "poll-interval", 5, "Polling interval in seconds for async mode")
	flag.IntVar(&maxPollRetry, "poll-max", 60, "Maximum number of polling attempts")
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
	// Construct the job status URL
	baseURL := cfg.API.URL
	if strings.HasSuffix(baseURL, "/analyze") {
		baseURL = baseURL[:len(baseURL)-len("/analyze")]
	}
	jobURL := fmt.Sprintf("%s/jobs/%s", baseURL, jobID)

	var lastCompletedItems int = 0
	var noProgressCounter int = 0

	for attempt := 0; attempt < maxPollRetry; attempt++ {
		log.Printf("Polling job status (attempt %d/%d)...", attempt+1, maxPollRetry)

		req, err := http.NewRequestWithContext(ctx, "GET", jobURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create job status request: %v", err)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to get job status: %v", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read job status response: %v", err)
		}

		// Try to parse the full response first (works if job is completed)
		var fullResult struct {
			JobID          string           `json:"job_id"`
			Status         string           `json:"status"`
			TotalItems     int              `json:"total_items"`
			CompletedItems int              `json:"completed_items"`
			FailedItems    int              `json:"failed_items"`
			Results        []pkg.ReportItem `json:"results"`
		}

		err = json.Unmarshal(body, &fullResult)

		// Check if we already have completed status and results
		if err == nil && (fullResult.Status == "completed" || fullResult.Status == "failed") && len(fullResult.Results) > 0 {
			log.Printf("Job complete: %s - %d/%d items processed, %d failed",
				fullResult.Status, fullResult.CompletedItems, fullResult.TotalItems, fullResult.FailedItems)
			return fullResult.Results, nil
		}

		// If not completed or couldn't parse full result, parse basic status
		var statusResp struct {
			JobID          string          `json:"job_id"`
			Status         string          `json:"status"`
			TotalItems     int             `json:"total_items"`
			CompletedItems int             `json:"completed_items"`
			FailedItems    int             `json:"failed_items"`
			Results        json.RawMessage `json:"results"`
		}

		err = json.Unmarshal(body, &statusResp)
		if err != nil {
			log.Printf("Warning: Failed to parse job status: %v", err)
			time.Sleep(time.Duration(pollInterval) * time.Second)
			continue
		}

		log.Printf("Job status: %s (%d/%d items processed, %d failed)",
			statusResp.Status, statusResp.CompletedItems, statusResp.TotalItems, statusResp.FailedItems)

		// See if we can parse results directly from the response
		if statusResp.Results != nil && len(statusResp.Results) > 2 { // More than just '[]'
			var reportItems []pkg.ReportItem
			if err := json.Unmarshal(statusResp.Results, &reportItems); err == nil && len(reportItems) > 0 {
				log.Printf("Successfully parsed %d report items from response", len(reportItems))
				if statusResp.CompletedItems == statusResp.TotalItems ||
					(statusResp.CompletedItems+statusResp.FailedItems >= statusResp.TotalItems) {
					return reportItems, nil
				}
			}
		}

		// If the status is still "processing"
		if statusResp.Status == "processing" {
			// Check if all items are processed, even if status hasn't been updated yet
			if statusResp.CompletedItems+statusResp.FailedItems >= statusResp.TotalItems {
				noProgressCounter++

				// If all items are processed but status hasn't changed for 3 consecutive polls,
				// consider the job complete and force a return with the results
				if noProgressCounter >= 3 {
					log.Printf("All items processed but job status still '%s'. Forcing completion after %d polls with no status change.",
						statusResp.Status, noProgressCounter)

					// Make a specific call to get the full job details
					// This is a more explicit call that should return complete data
					req, err := http.NewRequestWithContext(ctx, "GET",
						fmt.Sprintf("%s/jobs/%s?force_complete=true", baseURL, jobID), nil)
					if err == nil {
						forceResp, err := client.Do(req)
						if err == nil && forceResp.StatusCode == http.StatusOK {
							forceBody, err := io.ReadAll(forceResp.Body)
							forceResp.Body.Close()
							if err == nil {
								var forceResult struct {
									Results []pkg.ReportItem `json:"results"`
								}

								if err := json.Unmarshal(forceBody, &forceResult); err == nil && len(forceResult.Results) > 0 {
									log.Printf("Successfully retrieved %d report items by force completion",
										len(forceResult.Results))
									return forceResult.Results, nil
								}
							}
						}
					}

					// If we get here, we couldn't get the results by forcing completion
					// Just return an empty array rather than timing out
					log.Printf("WARNING: Forced completion, but couldn't retrieve results. Returning empty result set.")
					return []pkg.ReportItem{}, nil
				}
			} else {
				// Reset counter if we see progress
				if statusResp.CompletedItems > lastCompletedItems {
					lastCompletedItems = statusResp.CompletedItems
					noProgressCounter = 0
				}
			}

			// Wait before next poll
			time.Sleep(time.Duration(pollInterval) * time.Second)
			continue
		}

		// Handle non-processing status that's not already handled above
		if statusResp.Status != "processing" {
			// For completed or failed status but without results in direct response
			req, err := http.NewRequestWithContext(ctx, "GET", jobURL, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create final results request: %v", err)
			}

			finalResp, err := client.Do(req)
			if err != nil {
				return nil, fmt.Errorf("failed to get final results: %v", err)
			}

			finalBody, err := io.ReadAll(finalResp.Body)
			finalResp.Body.Close()
			if err != nil {
				return nil, fmt.Errorf("failed to read final results response: %v", err)
			}

			// Try to get results from the response
			var finalResult struct {
				Results []pkg.ReportItem `json:"results"`
			}
			if err := json.Unmarshal(finalBody, &finalResult); err == nil && len(finalResult.Results) > 0 {
				return finalResult.Results, nil
			}

			// If we still don't have results, return an empty array
			log.Printf("WARNING: Job is %s but no results found. Returning empty result set.", statusResp.Status)
			return []pkg.ReportItem{}, nil
		}
	}

	return nil, fmt.Errorf("maximum polling attempts reached - job may still be running")
}

func main() {
	// Parse command-line flags
	flag.Parse()

	// Show help if requested
	if len(os.Args) == 1 || (len(os.Args) == 2 && (os.Args[1] == "-h" || os.Args[1] == "--help")) {
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
		defaultConfig.Scan.Resources = []string{"ec2"}
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
		cfg.Scan.Resources = []string{"ec2"}
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

	// Create AWS service clients
	ec2Client := ec2.NewFromConfig(awsCfg)
	cwClient := cloudwatch.NewFromConfig(awsCfg)

	// Scan resources
	if containsResource(cfg.Scan.Resources, "ec2") {
		// Scan EC2 instances
		log.Println("Scanning EC2 instances...")
		instances, err := pkg.ListInstances(ctx, ec2Client, cwClient)
		if err != nil {
			log.Fatalf("Failed to scan EC2 instances: %v", err)
		}

		// Apply resource cap limit
		if len(instances) > cfg.Scan.Limit {
			log.Printf("Limiting analysis to %d instances (found %d)", cfg.Scan.Limit, len(instances))
			instances = instances[:cfg.Scan.Limit]
		}

		if len(instances) == 0 {
			log.Println("No running EC2 instances found.")
			return
		}

		// Prepare request payload
		requestPayload := map[string]interface{}{
			"instances": instances,
		}
		requestBody, err := json.Marshal(requestPayload)
		if err != nil {
			log.Fatalf("Failed to marshal request: %v", err)
		}

		// Create HTTP client
		client := &http.Client{
			Timeout: time.Duration(cfg.API.Timeout) * time.Second,
		}
		log.Printf("Using HTTP client with timeout: %s", client.Timeout)

		// Process based on mode (sync or async)
		if asyncMode {
			log.Printf("Using asynchronous mode for processing %d instances...", len(instances))

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
			log.Printf("Sending %d instances to GreenOps API for analysis with timeout of %d seconds...",
				len(instances), cfg.API.Timeout)
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
						log.Fatalf("API request timed out after %d retries. Try increasing the timeout with --timeout or reduce the number of instances with --limit", maxRetries)
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
					log.Fatalf("API service unavailable (503). The service might be experiencing high load or temporary issues with the underlying models. Try again later or with fewer instances.")
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

				// Use our formatter for better output
				pkg.FormatAnalysisReport(file, apiResponse.Report, false) // No colors in file output
				log.Printf("Results saved to %s", outputFile)
			} else {
				// Use colors if stdout is a terminal and colors are enabled
				useColors := isTerminal(os.Stdout) && cfg.Output.Colors

				// Print to console using our formatter
				pkg.FormatAnalysisReport(os.Stdout, apiResponse.Report, useColors)
			}
		}
	} else {
		log.Println("EC2 resource scan not enabled in configuration.")
	}
}

// Helper function to check if a resource type is enabled
func containsResource(resources []string, resource string) bool {
	for _, r := range resources {
		if r == resource {
			return true
		}
	}
	return false
}
