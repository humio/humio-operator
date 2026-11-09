/*
Copyright 2020 Humio https://humio.com

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	// load all auth plugins
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

var (
	// We override these using ldflags when running "go build"
	commit  = "none"
	date    = "unknown"
	version = "master"
)

func newKubernetesClientset() *k8s.Clientset {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}

	clientset, err := k8s.NewForConfig(cfg)
	if err != nil {
		panic(err.Error())
	}
	return clientset
}

// initMode looks up the availability zone of the Kubernetes node defined in environment variable NODE_NAME and saves
// the result to the file defined in environment variable TARGET_FILE
func initMode() {
	nodeName, found := os.LookupEnv("NODE_NAME")
	if !found || nodeName == "" {
		panic("environment variable NODE_NAME not set or empty")
	}

	targetFile, found := os.LookupEnv("TARGET_FILE")
	if !found || targetFile == "" {
		panic("environment variable TARGET_FILE not set or empty")
	}

	ctx := context.Background()

	clientset := newKubernetesClientset()

	node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		panic(err.Error())
	}
	zone, found := node.Labels[corev1.LabelZoneFailureDomainStable]
	if !found {
		zone = node.Labels[corev1.LabelZoneFailureDomain]
	}
	err = os.WriteFile(targetFile, []byte(zone), 0644) // #nosec G306
	if err != nil {
		panic(fmt.Sprintf("unable to write file with availability zone information: %s", err))
	}
}

// CheckResult represents the result of a dependency check
type CheckResult struct {
	Name  string
	Error error
}

// DependencyCheck interface
type DependencyCheck interface {
	Name() string
	Check(ctx context.Context) error
	GetTimeout() int
}

// KafkaCheck implementation
type KafkaCheck struct {
	Servers string
	Timeout int
}

func (k *KafkaCheck) Name() string    { return "Kafka" }
func (k *KafkaCheck) GetTimeout() int { return k.Timeout }

func (k *KafkaCheck) Check(ctx context.Context) error {
	if k.Servers == "" {
		return fmt.Errorf("KAFKA_SERVERS not configured")
	}

	// Suppress Sarama's default stderr logger to avoid leaking broker topology.
	sarama.Logger = noopLogger{}

	cfg := sarama.NewConfig()
	cfg.Net.DialTimeout = time.Duration(k.Timeout) * time.Second
	cfg.Metadata.Timeout = time.Duration(k.Timeout) * time.Second
	cfg.Version = sarama.V2_6_0_0 // Conservative version

	brokers := strings.Split(k.Servers, ",")

	// sarama does not support context cancellation; use a done channel to
	// enforce the parent context deadline around the blocking call.
	type result struct {
		client sarama.Client
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := sarama.NewClient(brokers, cfg)
		ch <- result{c, err}
	}()

	select {
	case <-ctx.Done():
		// Drain the channel and close any client that arrives to avoid leaking
		// open TCP connections to Kafka brokers.
		go func() {
			if r := <-ch; r.client != nil {
				_ = r.client.Close()
			}
		}()
		return fmt.Errorf("Kafka check cancelled: %w", ctx.Err())
	case r := <-ch:
		if r.err != nil {
			return fmt.Errorf("failed to connect to Kafka brokers %v: %w", brokers, r.err)
		}
		defer r.client.Close()

		topicsCh := make(chan error, 1)
		go func() {
			_, err := r.client.Topics()
			topicsCh <- err
		}()
		select {
		case <-ctx.Done():
			return fmt.Errorf("Kafka check cancelled: %w", ctx.Err())
		case err := <-topicsCh:
			if err != nil {
				return fmt.Errorf("failed to list topics from Kafka: %w", err)
			}
		}
	}

	return nil
}

// S3Check implementation
type S3Check struct {
	Bucket        string
	Region        string
	Endpoint      string
	PathStyleAccess bool
	AccessKey     string
	SecretKey     string
	Timeout       int
}

func (s *S3Check) Name() string    { return "S3" }
func (s *S3Check) GetTimeout() int { return s.Timeout }

func (s *S3Check) Check(ctx context.Context) error {
	if s.Bucket == "" {
		return fmt.Errorf("S3_STORAGE_BUCKET not configured")
	}

	// Load AWS config using the default credential chain (supports IRSA, env vars, instance profiles, etc.)
	var optFns []func(*awsconfig.LoadOptions) error
	if s.Region != "" {
		optFns = append(optFns, awsconfig.WithRegion(s.Region))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Override with static credentials only when explicitly provided.
	if s.AccessKey != "" && s.SecretKey != "" {
		awsCfg.Credentials = credentials.NewStaticCredentialsProvider(s.AccessKey, s.SecretKey, "")
	}

	if s.Endpoint != "" {
		// Validate the custom endpoint: only allow http/https schemes and
		// reject host-less URLs to block SSRF to internal metadata services.
		parsed, err := url.Parse(s.Endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("S3_STORAGE_ENDPOINT is not a valid http/https URL: %q", s.Endpoint)
		}
		awsCfg.BaseEndpoint = aws.String(s.Endpoint)
	}

	// Create S3 client — enable path-style when a custom endpoint is used or
	// when explicitly requested (e.g. MinIO).
	usePathStyle := s.PathStyleAccess || s.Endpoint != ""
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = usePathStyle
	})

	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(s.Bucket),
	})
	if err != nil {
		return fmt.Errorf("failed to access S3 bucket '%s': %w", s.Bucket, err)
	}

	return nil
}

// GCSCheck implementation
type GCSCheck struct {
	Bucket       string
	EndpointBase string // Optional endpoint override (e.g., for testing or self-hosted storage)
	Timeout      int
}

func (g *GCSCheck) Name() string    { return "GCS" }
func (g *GCSCheck) GetTimeout() int { return g.Timeout }

func (g *GCSCheck) Check(ctx context.Context) error {
	if g.Bucket == "" {
		return fmt.Errorf("GCP_STORAGE_BUCKET not configured")
	}

	var httpClient *http.Client
	var reqURL string

	if g.EndpointBase != "" {
		// Validate the custom endpoint: only allow http/https schemes and
		// reject host-less URLs to block SSRF to internal metadata services.
		parsed, err := url.Parse(g.EndpointBase)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("GCP_STORAGE_ENDPOINT_BASE is not a valid http/https URL: %q", g.EndpointBase)
		}

		// Use a plain HTTP client for custom endpoints (e.g. mock servers in tests).
		// GCP credentials are not available in non-GCP environments so we skip ADC here.
		httpClient = &http.Client{
			Timeout: time.Duration(g.Timeout) * time.Second,
		}
		// Point at the custom base; append the bucket path so the same
		// bucket-metadata response codes apply.
		reqURL = strings.TrimRight(g.EndpointBase, "/") + "/storage/v1/b/" + url.PathEscape(g.Bucket)
	} else {
		// Try to get Application Default Credentials (ADC)
		// This will work with:
		// - GOOGLE_APPLICATION_CREDENTIALS env var pointing to service account JSON
		// - GKE Workload Identity
		// - GCE instance metadata
		// - gcloud CLI credentials
		creds, err := google.FindDefaultCredentials(ctx)
		if err != nil {
			return fmt.Errorf("failed to find GCP credentials: %w", err)
		}
		if creds == nil {
			return fmt.Errorf("GCP credentials are nil despite no error")
		}

		httpClient = &http.Client{
			Timeout:   time.Duration(g.Timeout) * time.Second,
			Transport: &oauth2.Transport{Source: creds.TokenSource},
		}
		// Use GCS JSON API to check bucket access.
		reqURL = "https://storage.googleapis.com/storage/v1/b/" + url.PathEscape(g.Bucket)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create GCS request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to access GCS bucket '%s': %w", g.Bucket, err)
	}
	defer resp.Body.Close()

	// Discard response body to free up connection
	_, _ = io.Copy(io.Discard, resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("GCS bucket '%s' not found (404)", g.Bucket)
	case http.StatusForbidden:
		return fmt.Errorf("GCS bucket '%s' access forbidden - check credentials and permissions (403)", g.Bucket)
	case http.StatusUnauthorized:
		return fmt.Errorf("GCS bucket '%s' authentication failed - check credentials (401)", g.Bucket)
	default:
		return fmt.Errorf("unexpected status code %d when accessing GCS bucket '%s'", resp.StatusCode, g.Bucket)
	}
}

// noopLogger satisfies sarama.StdLogger and discards all output.
type noopLogger struct{}

func (noopLogger) Print(v ...any)                 {}
func (noopLogger) Printf(format string, v ...any) {}
func (noopLogger) Println(v ...any)               {}

func getEnvAsInt(key string, defaultValue int) int {
	valStr := os.Getenv(key)
	if valStr == "" {
		return defaultValue
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		fmt.Printf("Warning: invalid integer value for %s: %s, using default %d\n", key, valStr, defaultValue)
		return defaultValue
	}
	return val
}

// runChecksInParallel executes all checks concurrently and returns true if all pass.
// Each check receives the parent context so that the global timeout is honoured.
func runChecksInParallel(ctx context.Context, checks []DependencyCheck) bool {
	var wg sync.WaitGroup
	results := make(chan CheckResult, len(checks))

	for _, check := range checks {
		wg.Add(1)
		go func(c DependencyCheck) {
			defer wg.Done()
			checkCtx, cancel := context.WithTimeout(ctx, time.Duration(c.GetTimeout())*time.Second)
			defer cancel()

			err := c.Check(checkCtx)
			results <- CheckResult{Name: c.Name(), Error: err}
		}(check)
	}

	wg.Wait()
	close(results)

	var failedChecks []string

	for result := range results {
		if result.Error != nil {
			fmt.Printf("❌ %s check failed: %v\n", result.Name, result.Error)
			failedChecks = append(failedChecks, result.Name)
		} else {
			fmt.Printf("✓ %s check passed\n", result.Name)
		}
	}

	if len(failedChecks) > 0 {
		fmt.Printf("Failed checks: %s\n", strings.Join(failedChecks, ", "))
	}

	return len(failedChecks) == 0
}

func checkDependenciesMode() error {
	enabled := os.Getenv("DEPENDENCY_CHECK_ENABLED")
	if enabled != "true" {
		fmt.Println("Dependency checks not enabled, skipping")
		return nil
	}

	enforcement := os.Getenv("DEPENDENCY_CHECK_ENFORCEMENT")
	if enforcement == "" {
		enforcement = "required"
	}
	if enforcement == "disabled" {
		fmt.Println("Dependency check enforcement is disabled, skipping")
		return nil
	}

	fmt.Printf("=== Starting Dependency Checks (enforcement=%s) ===\n", enforcement)

	timeout := getEnvAsInt("DEPENDENCY_CHECK_TIMEOUT", 600)
	retryInterval := getEnvAsInt("DEPENDENCY_CHECK_RETRY_INTERVAL", 5)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	var checks []DependencyCheck

	// Per-check timeout: use DEPENDENCY_CHECK_PER_CHECK_TIMEOUT if set,
	// otherwise default to globalTimeout/3 (capped at 30s minimum) so that
	// a single slow check cannot consume the entire retry budget.
	perCheckTimeout := getEnvAsInt("DEPENDENCY_CHECK_PER_CHECK_TIMEOUT", 0)
	if perCheckTimeout <= 0 {
		perCheckTimeout = timeout / 3
		if perCheckTimeout < 30 {
			perCheckTimeout = 30
		}
		// Per-check timeout must not exceed the global timeout.
		if perCheckTimeout > timeout {
			perCheckTimeout = timeout
		}
	}

	if os.Getenv("CHECK_KAFKA") == "true" {
		checks = append(checks, &KafkaCheck{
			Servers: os.Getenv("KAFKA_SERVERS"),
			Timeout: perCheckTimeout,
		})
	}

	if os.Getenv("CHECK_S3") == "true" {
		pathStyle := os.Getenv("S3_STORAGE_PATH_STYLE_ACCESS") == "true"
		checks = append(checks, &S3Check{
			Bucket:          os.Getenv("S3_STORAGE_BUCKET"),
			Region:          os.Getenv("S3_STORAGE_REGION"),
			Endpoint:        os.Getenv("S3_STORAGE_ENDPOINT"),
			PathStyleAccess: pathStyle,
			AccessKey:       os.Getenv("S3_ACCESS_KEY_ID"),
			SecretKey:       os.Getenv("S3_SECRET_ACCESS_KEY"),
			Timeout:         perCheckTimeout,
		})
	}

	if os.Getenv("CHECK_GCS") == "true" {
		checks = append(checks, &GCSCheck{
			Bucket:       os.Getenv("GCP_STORAGE_BUCKET"),
			EndpointBase: os.Getenv("GCP_STORAGE_ENDPOINT_BASE"),
			Timeout:      perCheckTimeout,
		})
	}

	if len(checks) == 0 {
		fmt.Println("No dependency checks configured")
		return nil
	}

	ticker := time.NewTicker(time.Duration(retryInterval) * time.Second)
	defer ticker.Stop()

	attempt := 0
	for {
		attempt++
		fmt.Printf("\n--- Attempt %d ---\n", attempt)

		if runChecksInParallel(ctx, checks) {
			fmt.Println("\n=== All dependency checks passed! ===")
			return nil
		}

		// Reset the ticker so the cooldown is measured from when checks
		// completed, not from when the previous tick fired.
		ticker.Reset(time.Duration(retryInterval) * time.Second)
		fmt.Printf("Retrying in %ds...\n", retryInterval)
		select {
		case <-ctx.Done():
			if enforcement == "advisory" {
				fmt.Printf("WARNING: dependency check timeout after %d attempts (advisory mode, continuing startup)\n", attempt)
				return nil
			}
			return fmt.Errorf("dependency check timeout after %d attempts", attempt)
		case <-ticker.C:
		}
	}
}

func main() {
	fmt.Printf("Starting humio-operator-helper %s (%s on %s)\n", version, commit, date)
	mode, found := os.LookupEnv("MODE")
	if !found || mode == "" {
		panic("environment variable MODE not set or empty")
	}
	switch mode {
	case "init":
		initMode()
	case "init-with-checks":
		// Combined: zone detection + dependency checks
		initMode()
		if err := checkDependenciesMode(); err != nil {
			panic(fmt.Sprintf("dependency check mode failed: %v", err))
		}
	case "check-dependencies":
		// Standalone dependency-check mode for manual testing/debugging.
		// The operator always uses "init-with-checks" in production; this mode is
		// not set by any controller code path.
		if err := checkDependenciesMode(); err != nil {
			panic(fmt.Sprintf("dependency check mode failed: %v", err))
		}
	default:
		panic("unsupported mode")
	}

	fmt.Println("Init container completed successfully")
}
