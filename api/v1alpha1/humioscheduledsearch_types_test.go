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

package v1alpha1

import (
	"testing"
)

func TestParseTimeStringToSeconds(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int64
		wantErr  bool
	}{
		// Special case: "now"
		{
			name:     "now returns zero",
			input:    "now",
			expected: 0,
			wantErr:  false,
		},

		// Seconds
		{
			name:     "seconds - s",
			input:    "30s",
			expected: 30,
			wantErr:  false,
		},
		{
			name:     "seconds - sec",
			input:    "45sec",
			expected: 45,
			wantErr:  false,
		},
		{
			name:     "seconds - second",
			input:    "1second",
			expected: 1,
			wantErr:  false,
		},
		{
			name:     "seconds - seconds",
			input:    "120seconds",
			expected: 120,
			wantErr:  false,
		},

		// Minutes
		{
			name:     "minutes - m",
			input:    "5m",
			expected: 300, // 5 * 60
			wantErr:  false,
		},
		{
			name:     "minutes - min",
			input:    "10min",
			expected: 600, // 10 * 60
			wantErr:  false,
		},
		{
			name:     "minutes - minute",
			input:    "1minute",
			expected: 60,
			wantErr:  false,
		},
		{
			name:     "minutes - minutes",
			input:    "15minutes",
			expected: 900, // 15 * 60
			wantErr:  false,
		},

		// Hours
		{
			name:     "hours - h",
			input:    "2h",
			expected: 7200, // 2 * 3600
			wantErr:  false,
		},
		{
			name:     "hours - hour",
			input:    "1hour",
			expected: 3600,
			wantErr:  false,
		},
		{
			name:     "hours - hours",
			input:    "24hours",
			expected: 86400, // 24 * 3600
			wantErr:  false,
		},

		// Days
		{
			name:     "days - d",
			input:    "1d",
			expected: 86400, // 1 * 86400
			wantErr:  false,
		},
		{
			name:     "days - day",
			input:    "1day",
			expected: 86400,
			wantErr:  false,
		},
		{
			name:     "days - days",
			input:    "7days",
			expected: 604800, // 7 * 86400
			wantErr:  false,
		},

		// Weeks
		{
			name:     "weeks - w",
			input:    "1w",
			expected: 604800, // 1 * 604800
			wantErr:  false,
		},
		{
			name:     "weeks - week",
			input:    "2week",
			expected: 1209600, // 2 * 604800
			wantErr:  false,
		},
		{
			name:     "weeks - weeks",
			input:    "4weeks",
			expected: 2419200, // 4 * 604800
			wantErr:  false,
		},

		// Years
		{
			name:     "years - y",
			input:    "1y",
			expected: 31536000, // 1 * 31536000
			wantErr:  false,
		},
		{
			name:     "years - year",
			input:    "1year",
			expected: 31536000,
			wantErr:  false,
		},
		{
			name:     "years - years",
			input:    "2years",
			expected: 63072000, // 2 * 31536000
			wantErr:  false,
		},

		// Large numbers
		{
			name:     "large number",
			input:    "999h",
			expected: 3596400, // 999 * 3600
			wantErr:  false,
		},

		// Zero values
		{
			name:     "zero seconds",
			input:    "0s",
			expected: 0,
			wantErr:  false,
		},
		{
			name:     "zero minutes",
			input:    "0m",
			expected: 0,
			wantErr:  false,
		},

		// Error cases
		{
			name:     "empty string",
			input:    "",
			expected: 0,
			wantErr:  true,
		},
		{
			name:     "single character",
			input:    "s",
			expected: 0,
			wantErr:  true,
		},
		{
			name:     "no number",
			input:    "seconds",
			expected: 0,
			wantErr:  true,
		},
		{
			name:     "invalid unit",
			input:    "10x",
			expected: 0,
			wantErr:  true,
		},
		{
			name:     "unknown unit",
			input:    "5millennia",
			expected: 0,
			wantErr:  true,
		},
		{
			name:     "negative number not supported",
			input:    "-5m",
			expected: 0,
			wantErr:  true,
		},
		{
			name:     "decimal number not supported",
			input:    "1.5h",
			expected: 0,
			wantErr:  true,
		},
		{
			name:     "number only without unit",
			input:    "123",
			expected: 0,
			wantErr:  true,
		},
		{
			name:     "mixed case unit",
			input:    "5Min",
			expected: 0,
			wantErr:  true,
		},
		{
			name:     "space in string",
			input:    "5 minutes",
			expected: 0,
			wantErr:  true,
		},
		{
			name:     "multiple numbers",
			input:    "5m10s",
			expected: 0,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseTimeStringToSeconds(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseTimeStringToSeconds(%q) expected error, but got none", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("ParseTimeStringToSeconds(%q) unexpected error: %v", tt.input, err)
				}
				if result != tt.expected {
					t.Errorf("ParseTimeStringToSeconds(%q) = %d, expected %d", tt.input, result, tt.expected)
				}
			}
		})
	}
}

func TestParseSecondsToString(t *testing.T) {
	tests := []struct {
		name     string
		input    int64
		expected string
		wantErr  bool
	}{
		// Special cases
		{
			name:     "zero returns now",
			input:    0,
			expected: "now",
			wantErr:  false,
		},
		{
			name:     "negative returns now",
			input:    -100,
			expected: "now",
			wantErr:  false,
		},

		// Exact conversions (no remainder)
		{
			name:     "exact seconds",
			input:    30,
			expected: "30s",
			wantErr:  false,
		},
		{
			name:     "exact minutes",
			input:    300, // 5 * 60
			expected: "5m",
			wantErr:  false,
		},
		{
			name:     "exact hours",
			input:    7200, // 2 * 3600
			expected: "2h",
			wantErr:  false,
		},
		{
			name:     "exact days",
			input:    86400, // 1 * 86400
			expected: "1d",
			wantErr:  false,
		},
		{
			name:     "multiple days",
			input:    604800, // 7 * 86400
			expected: "7d",
			wantErr:  false,
		},

		// Additional edge cases to understand the algorithm
		{
			name:     "exactly 90 seconds (divisible by 60)",
			input:    90, // This should be 90s since 90%60 != 0 (90%60 = 30)
			expected: "90s",
			wantErr:  false,
		},
		{
			name:     "exactly 120 seconds (2 minutes)",
			input:    120, // 120%60 == 0, so this should be "2m"
			expected: "2m",
			wantErr:  false,
		},
		{
			name:     "non-exact minutes",
			input:    150, // 2.5 minutes, not divisible by 60 exactly, so returns 150s
			expected: "150s",
			wantErr:  false,
		},
		{
			name:     "90 minutes exactly",
			input:    5400, // 90 * 60 = 5400, exactly 90 minutes
			expected: "90m",
			wantErr:  false,
		},
		{
			name:     "25 hours exactly",
			input:    90000, // 25 * 3600 = 90000, exactly 25 hours
			expected: "25h",
			wantErr:  false,
		},

		// Large values
		{
			name:     "large value in days",
			input:    2592000, // 30 * 86400 (30 days)
			expected: "30d",
			wantErr:  false,
		},
		{
			name:     "large value in hours",
			input:    3600000, // 1000 * 3600 (1000 hours)
			expected: "1000h",
			wantErr:  false,
		},

		// Edge cases
		{
			name:     "one second",
			input:    1,
			expected: "1s",
			wantErr:  false,
		},
		{
			name:     "one minute",
			input:    60,
			expected: "1m",
			wantErr:  false,
		},
		{
			name:     "one hour",
			input:    3600,
			expected: "1h",
			wantErr:  false,
		},
		{
			name:     "one day",
			input:    86400,
			expected: "1d",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseSecondsToString(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseSecondsToString(%d) expected error, but got none", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("ParseSecondsToString(%d) unexpected error: %v", tt.input, err)
				}
				if result != tt.expected {
					t.Errorf("ParseSecondsToString(%d) = %q, expected %q", tt.input, result, tt.expected)
				}
			}
		})
	}
}

// TestRoundTripConversion tests that converting from string to seconds and back to string works correctly
func TestRoundTripConversion(t *testing.T) {
	testCases := []string{
		"now",
		"30s",
		"5m",
		"2h",
		"1d",
		"7d",
		"0s",
		"1s",
		"60s", // Should convert to 1m and back to 60s, not "1m"
	}

	for _, tc := range testCases {
		t.Run("roundtrip_"+tc, func(t *testing.T) {
			// Convert string to seconds
			seconds, err := ParseTimeStringToSeconds(tc)
			if err != nil {
				t.Fatalf("ParseTimeStringToSeconds(%q) failed: %v", tc, err)
			}

			// Convert seconds back to string
			result, err := ParseSecondsToString(seconds)
			if err != nil {
				t.Fatalf("ParseSecondsToString(%d) failed: %v", seconds, err)
			}

			// For exact conversions, we should get the same logical result
			// but the format might be different (e.g., "60s" -> 60 -> "1m")
			// So we verify by converting back again
			finalSeconds, err := ParseTimeStringToSeconds(result)
			if err != nil {
				t.Fatalf("Final ParseTimeStringToSeconds(%q) failed: %v", result, err)
			}

			if finalSeconds != seconds {
				t.Errorf("Round trip failed: %q -> %d -> %q -> %d", tc, seconds, result, finalSeconds)
			}
		})
	}
}
