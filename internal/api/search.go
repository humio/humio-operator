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

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Query represents a LogScale search query
type Query struct {
	QueryString                string            `json:"queryString"`
	Start                      string            `json:"start,omitempty"`
	End                        string            `json:"end,omitempty"`
	Live                       bool              `json:"isLive,omitempty"`
	ShowQueryEventDistribution bool              `json:"showQueryEventDistribution,omitempty"`
	TimeZoneOffsetMinutes      *int              `json:"timeZoneOffsetMinutes,omitempty"`
	Arguments                  map[string]string `json:"arguments,omitempty"`
}

// QueryJobResponse represents the response from creating a query job
type QueryJobResponse struct {
	ID string `json:"id"`
}

// QueryJobStatus represents the status of a running query job
type QueryJobStatus struct {
	ID             string `json:"id"`
	Done           bool   `json:"done"`
	State          string `json:"state"` // "RUNNING", "DONE", "FAILED", "CANCELLED"
	EventCount     int64  `json:"eventCount,omitempty"`
	ProcessedBytes int64  `json:"processedBytes,omitempty"`
	WorkDone       int64  `json:"workDone,omitempty"`
	TotalWork      int64  `json:"totalWork,omitempty"`
}

// QueryResult represents the results from a query job (matches CLI format)
type QueryResult struct {
	Cancelled bool         `json:"cancelled"`
	Done      bool         `json:"done"`
	Events    []QueryEvent `json:"events"`
	Metadata  QueryMeta    `json:"metaData"`
}

// QueryEvent represents a single event from query results
type QueryEvent map[string]interface{}

// QueryMeta represents metadata about query execution (matches CLI format)
type QueryMeta struct {
	EventCount       uint64                 `json:"eventCount"`
	ExtraData        map[string]interface{} `json:"extraData,omitempty"`
	FieldOrder       []string               `json:"fieldOrder,omitempty"`
	IsAggregate      bool                   `json:"isAggregate"`
	PollAfter        int                    `json:"pollAfter"`
	ProcessedBytes   uint64                 `json:"processedBytes"`
	ProcessedEvents  uint64                 `json:"processedEvents"`
	QueryStart       uint64                 `json:"queryStart"`
	QueryEnd         uint64                 `json:"queryEnd"`
	ResultBufferSize uint64                 `json:"resultBufferSize"`
	TimeMillis       uint64                 `json:"timeMillis"`
	TotalWork        uint64                 `json:"totalWork"`
	WorkDone         uint64                 `json:"workDone"`
}

// QueryError represents an error from query execution
type QueryError struct {
	Message string `json:"message"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

func (qe QueryError) Error() string {
	if qe.Line > 0 && qe.Column > 0 {
		return fmt.Sprintf("Query error at line %d, column %d: %s", qe.Line, qe.Column, qe.Message)
	}
	return fmt.Sprintf("Query error: %s", qe.Message)
}

// ExecuteLogScaleSearch executes a LogScale search query and returns the results
func (c *Client) ExecuteLogScaleSearch(ctx context.Context, repository string, query Query) (*QueryResult, error) {
	// Step 1: Create query job
	jobID, err := c.createQueryJob(ctx, repository, query)
	if err != nil {
		return nil, fmt.Errorf("failed to create query job: %w", err)
	}

	// Step 2: Poll for completion and get results
	defer func() {
		// Clean up the query job (ignore errors as this is cleanup)
		_ = c.deleteQueryJob(ctx, repository, jobID)
	}()

	return c.pollQueryJobResults(ctx, repository, jobID)
}

// createQueryJob creates a new query job and returns the job ID (based on CLI approach)
func (c *Client) createQueryJob(ctx context.Context, repository string, query Query) (string, error) {
	reqBody, err := json.Marshal(query)
	if err != nil {
		return "", fmt.Errorf("failed to marshal query request: %w", err)
	}

	path := fmt.Sprintf("api/v1/repositories/%s/queryjobs", repository)
	resp, err := c.HTTPRequestContext(ctx, http.MethodPost, path, bytes.NewReader(reqBody), JSONContentType)
	if err != nil {
		return "", fmt.Errorf("failed to send query job request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusBadRequest:
		// Handle 400 errors like the CLI - read the body for detailed error
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("query job creation failed with status 400, could not read error details: %w", err)
		}
		return "", QueryError{Message: fmt.Sprintf("Bad request: %s", string(body))}
	case http.StatusOK:
		// Success case - continue to decode response
	default:
		return "", fmt.Errorf("query job creation failed with status: %d", resp.StatusCode)
	}

	var jobResp QueryJobResponse
	if err := json.NewDecoder(resp.Body).Decode(&jobResp); err != nil {
		return "", fmt.Errorf("failed to decode query job response: %w", err)
	}

	return jobResp.ID, nil
}

// pollQueryJobResults polls for query completion and returns the results (CLI approach)
func (c *Client) pollQueryJobResults(ctx context.Context, repository, jobID string) (*QueryResult, error) {
	timeout := time.After(30 * time.Second) // Default timeout, should be configurable
	var nextPoll time.Time

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			return nil, fmt.Errorf("query job %s timed out after 30 seconds", jobID)
		case <-time.After(time.Until(nextPoll)):
			// Poll the query job using the CLI approach - single endpoint
			result, err := c.pollQueryJob(ctx, repository, jobID)
			if err != nil {
				return nil, fmt.Errorf("failed to poll query job: %w", err)
			}

			// Set next poll time based on result metadata (like CLI does)
			nextPoll = time.Now().Add(time.Duration(result.Metadata.PollAfter) * time.Millisecond)

			// Check if query is done
			if result.Done {
				return result, nil
			}

			// Check for cancelled state
			if result.Cancelled {
				return nil, &QueryError{Message: "query job was cancelled"}
			}
		}
	}
}

// pollQueryJob polls a single query job (matches CLI PollContext method)
func (c *Client) pollQueryJob(ctx context.Context, repository, jobID string) (*QueryResult, error) {
	path := fmt.Sprintf("api/v1/repositories/%s/queryjobs/%s", repository, jobID)
	resp, err := c.HTTPRequestContext(ctx, http.MethodGet, path, nil, JSONContentType)
	if err != nil {
		return nil, fmt.Errorf("failed to poll query job: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to poll query job, status code: %d", resp.StatusCode)
	}

	var result QueryResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode query job poll result: %w", err)
	}

	return &result, nil
}

// deleteQueryJob deletes a query job (cleanup)
func (c *Client) deleteQueryJob(ctx context.Context, repository, jobID string) error {
	path := fmt.Sprintf("api/v1/repositories/%s/queryjobs/%s", repository, jobID)
	resp, err := c.HTTPRequestContext(ctx, http.MethodDelete, path, nil, "")
	if err != nil {
		return fmt.Errorf("failed to delete query job: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Don't error on deletion failures as this is cleanup
	return nil
}

// ExecuteLogScaleSearchWithTimeout executes a search with a configurable timeout
func (c *Client) ExecuteLogScaleSearchWithTimeout(ctx context.Context, repository string, query Query, timeout time.Duration) (*QueryResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return c.ExecuteLogScaleSearch(ctx, repository, query)
}
