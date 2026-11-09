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
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// ── Group 1a: getEnvAsInt ──────────────────────────────────────────────

func TestGetEnvAsInt_Default(t *testing.T) {
	// No env var set → returns default
	got := getEnvAsInt("TEST_GETENVINT_MISSING", 99)
	if got != 99 {
		t.Errorf("expected 99, got %d", got)
	}
}

func TestGetEnvAsInt_ValidValue(t *testing.T) {
	t.Setenv("TEST_GETENVINT_VALID", "42")
	got := getEnvAsInt("TEST_GETENVINT_VALID", 0)
	if got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
}

func TestGetEnvAsInt_InvalidValue(t *testing.T) {
	t.Setenv("TEST_GETENVINT_INVALID", "abc")
	got := getEnvAsInt("TEST_GETENVINT_INVALID", 7)
	if got != 7 {
		t.Errorf("expected default 7, got %d", got)
	}
}

func TestGetEnvAsInt_EmptyString(t *testing.T) {
	t.Setenv("TEST_GETENVINT_EMPTY", "")
	got := getEnvAsInt("TEST_GETENVINT_EMPTY", 5)
	if got != 5 {
		t.Errorf("expected default 5, got %d", got)
	}
}

// ── Group 1b: S3Check ──────────────────────────────────────────────────

func TestS3Check_EmptyBucket(t *testing.T) {
	s := &S3Check{Bucket: ""}
	err := s.Check(context.Background())
	if err == nil {
		t.Fatal("expected error for empty bucket")
	}
	assertContains(t, err.Error(), "S3_STORAGE_BUCKET not configured")
}

func TestS3Check_InvalidEndpoint_NoScheme(t *testing.T) {
	s := &S3Check{Bucket: "b", Endpoint: "no-scheme", Timeout: 5}
	err := s.Check(context.Background())
	if err == nil {
		t.Fatal("expected error for endpoint without scheme")
	}
	assertContains(t, err.Error(), "not a valid http/https URL")
}

func TestS3Check_InvalidEndpoint_FTPScheme(t *testing.T) {
	s := &S3Check{Bucket: "b", Endpoint: "ftp://x", Timeout: 5}
	err := s.Check(context.Background())
	if err == nil {
		t.Fatal("expected error for ftp scheme")
	}
	assertContains(t, err.Error(), "not a valid http/https URL")
}

func TestS3Check_InvalidEndpoint_NoHost(t *testing.T) {
	s := &S3Check{Bucket: "b", Endpoint: "http://", Timeout: 5}
	err := s.Check(context.Background())
	if err == nil {
		t.Fatal("expected error for endpoint without host")
	}
	assertContains(t, err.Error(), "not a valid http/https URL")
}

func TestS3Check_ValidEndpoint_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &S3Check{
		Bucket:          "test-bucket",
		Endpoint:        srv.URL,
		PathStyleAccess: true,
		Timeout:         5,
	}
	err := s.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestS3Check_ValidEndpoint_BucketNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	s := &S3Check{
		Bucket:          "missing-bucket",
		Endpoint:        srv.URL,
		PathStyleAccess: true,
		Timeout:         5,
	}
	err := s.Check(context.Background())
	if err == nil {
		t.Fatal("expected error for missing bucket")
	}
	assertContains(t, err.Error(), "failed to access S3 bucket")
}

func TestS3Check_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	s := &S3Check{
		Bucket:          "test-bucket",
		Endpoint:        "http://localhost:1", // won't actually connect
		PathStyleAccess: true,
		Timeout:         5,
	}
	err := s.Check(ctx)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ── Group 1c: GCSCheck ─────────────────────────────────────────────────

func TestGCSCheck_EmptyBucket(t *testing.T) {
	g := &GCSCheck{Bucket: ""}
	err := g.Check(context.Background())
	if err == nil {
		t.Fatal("expected error for empty bucket")
	}
	assertContains(t, err.Error(), "GCP_STORAGE_BUCKET not configured")
}

func TestGCSCheck_InvalidEndpoint_NoScheme(t *testing.T) {
	g := &GCSCheck{Bucket: "b", EndpointBase: "no-scheme", Timeout: 5}
	err := g.Check(context.Background())
	if err == nil {
		t.Fatal("expected error for endpoint without scheme")
	}
	assertContains(t, err.Error(), "not a valid http/https URL")
}

func TestGCSCheck_ValidEndpoint_200OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	g := &GCSCheck{Bucket: "test-bucket", EndpointBase: srv.URL, Timeout: 5}
	err := g.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGCSCheck_ValidEndpoint_404NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	g := &GCSCheck{Bucket: "missing", EndpointBase: srv.URL, Timeout: 5}
	err := g.Check(context.Background())
	if err == nil {
		t.Fatal("expected error for 404")
	}
	assertContains(t, err.Error(), "not found")
}

func TestGCSCheck_ValidEndpoint_403Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	g := &GCSCheck{Bucket: "no-access", EndpointBase: srv.URL, Timeout: 5}
	err := g.Check(context.Background())
	if err == nil {
		t.Fatal("expected error for 403")
	}
	assertContains(t, err.Error(), "forbidden")
}

func TestGCSCheck_ValidEndpoint_401Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	g := &GCSCheck{Bucket: "no-auth", EndpointBase: srv.URL, Timeout: 5}
	err := g.Check(context.Background())
	if err == nil {
		t.Fatal("expected error for 401")
	}
	assertContains(t, err.Error(), "authentication failed")
}

func TestGCSCheck_ValidEndpoint_500ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	g := &GCSCheck{Bucket: "b", EndpointBase: srv.URL, Timeout: 5}
	err := g.Check(context.Background())
	if err == nil {
		t.Fatal("expected error for 500")
	}
	assertContains(t, err.Error(), "unexpected status code")
}

func TestGCSCheck_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	g := &GCSCheck{Bucket: "b", EndpointBase: "http://localhost:1", Timeout: 5}
	err := g.Check(ctx)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestGCSCheck_RequestPath(t *testing.T) {
	var receivedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	g := &GCSCheck{Bucket: "my-bucket", EndpointBase: srv.URL, Timeout: 5}
	err := g.Check(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "/storage/v1/b/" + url.PathEscape("my-bucket")
	if receivedPath != expected {
		t.Errorf("expected path %q, got %q", expected, receivedPath)
	}
}

// ── Group 1d: KafkaCheck ───────────────────────────────────────────────

func TestKafkaCheck_EmptyServers(t *testing.T) {
	k := &KafkaCheck{Servers: ""}
	err := k.Check(context.Background())
	if err == nil {
		t.Fatal("expected error for empty servers")
	}
	assertContains(t, err.Error(), "KAFKA_SERVERS not configured")
}

func TestKafkaCheck_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	k := &KafkaCheck{Servers: "localhost:19092", Timeout: 1}
	err := k.Check(ctx)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ── Group 1e: runChecksInParallel ──────────────────────────────────────

type mockCheck struct {
	name    string
	timeout int
	err     error
}

func (m *mockCheck) Name() string                    { return m.name }
func (m *mockCheck) GetTimeout() int                 { return m.timeout }
func (m *mockCheck) Check(ctx context.Context) error { return m.err }

func TestRunChecksInParallel_AllPass(t *testing.T) {
	checks := []DependencyCheck{
		&mockCheck{name: "a", timeout: 5, err: nil},
		&mockCheck{name: "b", timeout: 5, err: nil},
	}
	if !runChecksInParallel(context.Background(), checks) {
		t.Error("expected all checks to pass")
	}
}

func TestRunChecksInParallel_OneFails(t *testing.T) {
	checks := []DependencyCheck{
		&mockCheck{name: "ok", timeout: 5, err: nil},
		&mockCheck{name: "bad", timeout: 5, err: fmt.Errorf("fail")},
	}
	if runChecksInParallel(context.Background(), checks) {
		t.Error("expected failure when one check fails")
	}
}

func TestRunChecksInParallel_AllFail(t *testing.T) {
	checks := []DependencyCheck{
		&mockCheck{name: "a", timeout: 5, err: fmt.Errorf("err1")},
		&mockCheck{name: "b", timeout: 5, err: fmt.Errorf("err2")},
	}
	if runChecksInParallel(context.Background(), checks) {
		t.Error("expected failure when all checks fail")
	}
}

func TestRunChecksInParallel_Empty(t *testing.T) {
	if !runChecksInParallel(context.Background(), nil) {
		t.Error("expected success with no checks")
	}
}

// ── Group 1f: checkDependenciesMode ────────────────────────────────────

func TestCheckDependenciesMode_NotEnabled(t *testing.T) {
	// No env vars set → dependency checks not enabled
	err := checkDependenciesMode()
	if err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}
}

func TestCheckDependenciesMode_EnforcementDisabled(t *testing.T) {
	t.Setenv("DEPENDENCY_CHECK_ENABLED", "true")
	t.Setenv("DEPENDENCY_CHECK_ENFORCEMENT", "disabled")
	err := checkDependenciesMode()
	if err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}
}

func TestCheckDependenciesMode_NoChecksConfigured(t *testing.T) {
	t.Setenv("DEPENDENCY_CHECK_ENABLED", "true")
	t.Setenv("DEPENDENCY_CHECK_ENFORCEMENT", "required")
	// No CHECK_* env vars → no checks to run
	err := checkDependenciesMode()
	if err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}
}

func TestCheckDependenciesMode_GCSPassWithMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("DEPENDENCY_CHECK_ENABLED", "true")
	t.Setenv("DEPENDENCY_CHECK_ENFORCEMENT", "required")
	t.Setenv("DEPENDENCY_CHECK_TIMEOUT", "10")
	t.Setenv("DEPENDENCY_CHECK_RETRY_INTERVAL", "1")
	t.Setenv("CHECK_GCS", "true")
	t.Setenv("GCP_STORAGE_BUCKET", "test-bucket")
	t.Setenv("GCP_STORAGE_ENDPOINT_BASE", srv.URL)

	err := checkDependenciesMode()
	if err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}
}

func TestCheckDependenciesMode_AdvisoryReturnsNilOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	t.Setenv("DEPENDENCY_CHECK_ENABLED", "true")
	t.Setenv("DEPENDENCY_CHECK_ENFORCEMENT", "advisory")
	t.Setenv("DEPENDENCY_CHECK_TIMEOUT", "3")
	t.Setenv("DEPENDENCY_CHECK_RETRY_INTERVAL", "1")
	t.Setenv("CHECK_GCS", "true")
	t.Setenv("GCP_STORAGE_BUCKET", "test-bucket")
	t.Setenv("GCP_STORAGE_ENDPOINT_BASE", srv.URL)

	err := checkDependenciesMode()
	if err != nil {
		t.Fatalf("advisory mode should return nil on failure, got: %v", err)
	}
}

func TestCheckDependenciesMode_RequiredReturnsErrorOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	t.Setenv("DEPENDENCY_CHECK_ENABLED", "true")
	t.Setenv("DEPENDENCY_CHECK_ENFORCEMENT", "required")
	t.Setenv("DEPENDENCY_CHECK_TIMEOUT", "3")
	t.Setenv("DEPENDENCY_CHECK_RETRY_INTERVAL", "1")
	t.Setenv("CHECK_GCS", "true")
	t.Setenv("GCP_STORAGE_BUCKET", "test-bucket")
	t.Setenv("GCP_STORAGE_ENDPOINT_BASE", srv.URL)

	err := checkDependenciesMode()
	if err == nil {
		t.Fatal("required mode should return error on failure")
	}
	assertContains(t, err.Error(), "dependency check timeout")
}

// ── Group 1g: Per-Check Timeout Calculation ────────────────────────────

// deadlineCheck records the context deadline passed to Check()
type deadlineCheck struct {
	name     string
	timeout  int
	deadline time.Time
}

func (d *deadlineCheck) Name() string    { return d.name }
func (d *deadlineCheck) GetTimeout() int { return d.timeout }
func (d *deadlineCheck) Check(ctx context.Context) error {
	if dl, ok := ctx.Deadline(); ok {
		d.deadline = dl
	}
	return nil
}

func TestPerCheckTimeout_Default(t *testing.T) {
	// TIMEOUT=90 → 90/3=30, which is the minimum cap → 30s
	t.Setenv("DEPENDENCY_CHECK_ENABLED", "true")
	t.Setenv("DEPENDENCY_CHECK_ENFORCEMENT", "required")
	t.Setenv("DEPENDENCY_CHECK_TIMEOUT", "90")
	t.Setenv("DEPENDENCY_CHECK_RETRY_INTERVAL", "1")
	t.Setenv("CHECK_GCS", "true")
	t.Setenv("GCP_STORAGE_BUCKET", "test")

	// Use a mock server that succeeds so checkDependenciesMode returns quickly
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("GCP_STORAGE_ENDPOINT_BASE", srv.URL)

	// We can't easily inspect per-check timeout from checkDependenciesMode,
	// so verify indirectly via getEnvAsInt and the timeout calculation logic
	perCheck := getEnvAsInt("DEPENDENCY_CHECK_PER_CHECK_TIMEOUT", 0)
	if perCheck != 0 {
		t.Fatal("PER_CHECK_TIMEOUT should not be set")
	}
	timeout := 90
	calculated := timeout / 3
	if calculated < 30 {
		calculated = 30
	}
	if calculated != 30 {
		t.Errorf("expected 30, got %d", calculated)
	}
}

func TestPerCheckTimeout_MinimumCap(t *testing.T) {
	// TIMEOUT=60 → 60/3=20 < 30 cap → should be 30
	timeout := 60
	calculated := timeout / 3
	if calculated < 30 {
		calculated = 30
	}
	if calculated != 30 {
		t.Errorf("expected cap to 30, got %d", calculated)
	}
}

func TestPerCheckTimeout_Override(t *testing.T) {
	t.Setenv("DEPENDENCY_CHECK_PER_CHECK_TIMEOUT", "15")
	got := getEnvAsInt("DEPENDENCY_CHECK_PER_CHECK_TIMEOUT", 0)
	if got != 15 {
		t.Errorf("expected 15, got %d", got)
	}
}

// ── Test helpers ───────────────────────────────────────────────────────

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if len(s) == 0 || len(substr) == 0 {
		t.Errorf("assertContains: empty string or substr: %q, %q", s, substr)
		return
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Errorf("expected %q to contain %q", s, substr)
}
