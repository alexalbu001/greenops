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
)

// ServerResponse represents the API response format
type ServerResponse struct {
	Report []pkg.ReportItem `json:"report"`
}

func init() {
	// Define command-line flags
	flag.StringVar(&configFile, "config", "", "Path to configuration file")
	flag.BoolVar(&generateConf, "init", false, "Generate a default configuration file")
	flag.StringVar(&apiURL, "api", "https://-.execute-api.eu-west-1.amazonaws.com/analyze", "GreenOps API URL")
	flag.StringVar(&region, "region", "", "AWS Region (defaults to AWS_REGION env var or config file)")
	flag.StringVar(&profile, "profile", "", "AWS Profile (defaults to AWS_PROFILE env var or default profile)")
	flag.StringVar(&outputFile, "output", "", "Save results to file (default outputs to stdout)")
	flag.BoolVar(&debug, "debug", false, "Enable debug logging")
	flag.IntVar(&timeout, "timeout", 60, "API request timeout in seconds")
	flag.IntVar(&resourceCap, "limit", 10, "Maximum number of resources to scan")
	flag.BoolVar(&noColor, "no-color", false, "Disable colorized output")
}

// isTerminal detects if the output is going to a terminal
func isTerminal(f *os.File) bool {
	fileInfo, err := f.Stat()
	if err != nil {
		return false
	}
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

func main() {
	// Parse command-line flags
	flag.Parse()

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
		defaultConfig.API.URL = "https://-.execute-api.eu-west-1.amazonaws.com/analyze"
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

		// Create HTTP request with timeout
		log.Printf("Sending %d instances to GreenOps API for analysis with timeout of %d seconds...", len(instances), cfg.API.Timeout)
		httpCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.API.Timeout)*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(httpCtx, "POST", cfg.API.URL, bytes.NewBuffer(requestBody))
		if err != nil {
			log.Fatalf("Failed to create HTTP request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")

		// Send the request - UPDATED WITH EXPLICIT TIMEOUT
		client := &http.Client{
			Timeout: time.Duration(cfg.API.Timeout) * time.Second, // Explicit timeout setting
		}
		log.Printf("Using HTTP client with timeout: %s", client.Timeout)

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
