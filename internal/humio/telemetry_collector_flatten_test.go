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

package humio

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFlattenRepositoryUsageData(t *testing.T) {
	h := &ClientConfig{}
	timestamp := time.Now()
	clusterID := "test-cluster"

	// Create test repository usage data with nested arrays
	repositoryUsage := &TelemetryRepositoryUsageMetrics{
		TotalRepositories: 3,
		Repositories: []RepositoryUsage{
			{
				Name:              "repo1",
				IngestVolumeGB24h: 10.5,
				EventCount24h:     1000,
				RetentionDays:     30,
				StorageUsageGB:    50.0,
				LastActivityTime:  timestamp,
				Dataspace:         "repo1",
			},
			{
				Name:              "repo2",
				IngestVolumeGB24h: 5.2,
				EventCount24h:     500,
				RetentionDays:     7,
				StorageUsageGB:    25.0,
				LastActivityTime:  timestamp,
				Dataspace:         "repo2",
			},
		},
		TopRepositories: []RepositoryUsage{
			{
				Name:              "repo1",
				IngestVolumeGB24h: 10.5,
				EventCount24h:     1000,
				RetentionDays:     30,
				StorageUsageGB:    50.0,
				LastActivityTime:  timestamp,
				Dataspace:         "repo1",
			},
		},
	}

	// Flatten the data
	repositoryID := "test-repository-id"
	payloads := h.FlattenRepositoryUsageData(timestamp, clusterID, repositoryID, repositoryUsage, nil)

	// Verify we got the expected number of events:
	// 1 summary + 2 repositories + 1 top repository = 4 total events
	assert.Len(t, payloads, 4, "Should create 4 separate events")

	// Verify summary event
	summaryPayload := payloads[0]
	assert.Equal(t, "repository_usage", summaryPayload.CollectionType)
	summaryData, ok := summaryPayload.Data.(map[string]interface{})
	assert.True(t, ok, "Summary data should be a map")
	assert.Equal(t, 3, summaryData["total_repositories"])
	assert.Equal(t, "repository_usage_summary", summaryData["event_type"])

	// Verify first repository event (flattened fields)
	repo1Payload := payloads[1]
	assert.Equal(t, "repository_usage", repo1Payload.CollectionType)
	repo1Data, ok := repo1Payload.Data.(map[string]interface{})
	assert.True(t, ok, "Repository data should be a map")

	// Check that nested fields are now flattened
	assert.Equal(t, "repo1", repo1Data["name"])
	assert.Equal(t, 10.5, repo1Data["ingest_volume_gb_24h"])
	assert.Equal(t, int64(1000), repo1Data["event_count_24h"])
	assert.Equal(t, 30, repo1Data["retention_days"])
	assert.Equal(t, 50.0, repo1Data["storage_usage_gb"])
	assert.Equal(t, "repository_usage_detail", repo1Data["event_type"])

	// Verify that dataspace is now a single string field
	assert.Equal(t, "repo1", repo1Data["dataspace"])

	// Verify second repository event
	repo2Payload := payloads[2]
	repo2Data, ok := repo2Payload.Data.(map[string]interface{})
	assert.True(t, ok, "Repository data should be a map")
	assert.Equal(t, "repo2", repo2Data["name"])
	assert.Equal(t, 5.2, repo2Data["ingest_volume_gb_24h"])
	assert.Equal(t, "repository_usage_detail", repo2Data["event_type"])

	// Verify top repository event
	topRepoPayload := payloads[3]
	topRepoData, ok := topRepoPayload.Data.(map[string]interface{})
	assert.True(t, ok, "Top repository data should be a map")
	assert.Equal(t, "repo1", topRepoData["name"])
	assert.Equal(t, "top_repository_usage", topRepoData["event_type"])

	// Verify that all events have the same basic metadata
	for _, payload := range payloads {
		assert.Equal(t, timestamp, payload.Timestamp)
		assert.Equal(t, clusterID, payload.ClusterID)
		assert.Equal(t, "repository_usage", payload.CollectionType)
		assert.Equal(t, "json", payload.SourceType)
	}
}

func TestFlattenRepositoryUsageData_EmptyRepositories(t *testing.T) {
	h := &ClientConfig{}
	timestamp := time.Now()
	clusterID := "test-cluster"

	// Create test data with no repositories
	repositoryUsage := &TelemetryRepositoryUsageMetrics{
		TotalRepositories: 0,
		Repositories:      []RepositoryUsage{},
		TopRepositories:   []RepositoryUsage{},
	}

	// Flatten the data
	repositoryID := "test-repository-id"
	payloads := h.FlattenRepositoryUsageData(timestamp, clusterID, repositoryID, repositoryUsage, nil)

	// Should still create summary event
	assert.Len(t, payloads, 1, "Should create 1 summary event even with no repositories")

	summaryPayload := payloads[0]
	summaryData, ok := summaryPayload.Data.(map[string]interface{})
	assert.True(t, ok, "Summary data should be a map")
	assert.Equal(t, 0, summaryData["total_repositories"])
	assert.Equal(t, "repository_usage_summary", summaryData["event_type"])
}
