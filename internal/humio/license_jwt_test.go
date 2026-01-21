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
)

func TestGetJWTLicenseFields(t *testing.T) {
	// Test with the expired JWT token (safe for repository)
	expiredJWT := "eyJ0eXAiOiJKV1QiLCJhbGciOiJFUzUxMiJ9.eyJpc09lbSI6ZmFsc2UsImF1ZCI6Ikh1bWlvLWxpY2Vuc2UtY2hlY2siLCJzdWIiOiJIdW1pbyBFMkUgdGVzdHMiLCJ1aWQiOiJGUXNvWlM3Yk1PUldrbEtGIiwibWF4VXNlcnMiOjEwLCJhbGxvd1NBQVMiOnRydWUsIm1heENvcmVzIjoxLCJ2YWxpZFVudGlsIjoxNzQzMTY2ODAwLCJleHAiOjE3NzQ1OTMyOTcsImlzVHJpYWwiOmZhbHNlLCJpYXQiOjE2Nzk5ODUyOTcsIm1heEluZ2VzdEdiUGVyRGF5IjoxfQ.someinvalidsignature"

	fields, err := GetJWTLicenseFields(expiredJWT)
	if err != nil {
		t.Fatalf("Expected successful JWT parsing, got error: %v", err)
	}

	// Verify required fields (updated for expired license)
	if fields.UID != "FQsoZS7bMORWklKF" {
		t.Errorf("Expected UID 'FQsoZS7bMORWklKF', got '%s'", fields.UID)
	}

	if fields.Subject != "Humio E2E tests" {
		t.Errorf("Expected Subject 'Humio E2E tests', got '%s'", fields.Subject)
	}

	// Verify the key field we're after - maxIngestGbPerDay
	if fields.MaxIngestGbPerDay == nil {
		t.Fatal("Expected MaxIngestGbPerDay to be present, got nil")
	}
	if *fields.MaxIngestGbPerDay != 1.0 {
		t.Errorf("Expected MaxIngestGbPerDay 1.0, got %f", *fields.MaxIngestGbPerDay)
	}

	// Verify MaxCores
	if fields.MaxCores == nil {
		t.Fatal("Expected MaxCores to be present, got nil")
	}
	if *fields.MaxCores != 1 {
		t.Errorf("Expected MaxCores 1, got %d", *fields.MaxCores)
	}

	// Verify ValidUntil timestamp (updated for expired license)
	if fields.ValidUntil == nil {
		t.Fatal("Expected ValidUntil to be present, got nil")
	}
	validUntilTime := time.Unix(*fields.ValidUntil, 0)
	expectedTime := time.Unix(1743166800, 0)
	if !validUntilTime.Equal(expectedTime) {
		t.Errorf("Expected ValidUntil %v, got %v", expectedTime, validUntilTime)
	}

	t.Logf("Successfully parsed JWT license fields:")
	t.Logf("  UID: %s", fields.UID)
	t.Logf("  Subject: %s", fields.Subject)
	t.Logf("  MaxIngestGbPerDay: %f", *fields.MaxIngestGbPerDay)
	t.Logf("  MaxCores: %d", *fields.MaxCores)
	t.Logf("  ValidUntil: %v", validUntilTime)
}

func TestGetJWTLicenseFields_InvalidJWT(t *testing.T) {
	testCases := []struct {
		name string
		jwt  string
	}{
		{
			name: "empty string",
			jwt:  "",
		},
		{
			name: "invalid format",
			jwt:  "not.a.valid.jwt",
		},
		{
			name: "malformed base64",
			jwt:  "invalid.base64!@#.signature",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := GetJWTLicenseFields(tc.jwt)
			if err == nil {
				t.Errorf("Expected error for invalid JWT '%s', but got none", tc.name)
			}
		})
	}
}

func TestGetJWTLicenseFields_BackwardCompatibility(t *testing.T) {
	// Test that the old GetLicenseUIDFromLicenseString function still works
	expiredJWT := "eyJ0eXAiOiJKV1QiLCJhbGciOiJFUzUxMiJ9.eyJpc09lbSI6ZmFsc2UsImF1ZCI6Ikh1bWlvLWxpY2Vuc2UtY2hlY2siLCJzdWIiOiJIdW1pbyBFMkUgdGVzdHMiLCJ1aWQiOiJGUXNvWlM3Yk1PUldrbEtGIiwibWF4VXNlcnMiOjEwLCJhbGxvd1NBQVMiOnRydWUsIm1heENvcmVzIjoxLCJ2YWxpZFVudGlsIjoxNzQzMTY2ODAwLCJleHAiOjE3NzQ1OTMyOTcsImlzVHJpYWwiOmZhbHNlLCJpYXQiOjE2Nzk5ODUyOTcsIm1heEluZ2VzdEdiUGVyRGF5IjoxfQ.someinvalidsignature"

	uid, err := GetLicenseUIDFromLicenseString(expiredJWT)
	if err != nil {
		t.Fatalf("Expected successful UID extraction, got error: %v", err)
	}

	if uid != "FQsoZS7bMORWklKF" {
		t.Errorf("Expected UID 'FQsoZS7bMORWklKF', got '%s'", uid)
	}
}
