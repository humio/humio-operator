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
	"encoding/json"
	"strconv"
	"time"
)

// SanitizeTimestamps recursively converts Unix timestamps to ISO format strings
// This is primarily used for legacy timestamp fields and data from external sources
func SanitizeTimestamps(data interface{}) interface{} {
	switch v := data.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for key, value := range v {
			// Handle known timestamp fields that might contain Unix timestamps
			if IsTimestampField(key) {
				result[key] = SanitizeTimestampValue(value)
			} else {
				result[key] = SanitizeTimestamps(value)
			}
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, value := range v {
			result[i] = SanitizeTimestamps(value)
		}
		return result
	default:
		// For all other data types (strings, numbers, etc.), return as-is
		// We no longer need to guess which numbers might be timestamps
		return v
	}
}

// IsTimestampField checks if a field name represents a timestamp that might need sanitization
func IsTimestampField(fieldName string) bool {
	timestampFields := []string{
		"last_activity_time",
		"event_time",
		"created_at",
		"updated_at",
		// Note: @timestamp is handled by the struct JSON tags and doesn't need sanitization
	}

	for _, field := range timestampFields {
		if fieldName == field {
			return true
		}
	}
	return false
}

// SanitizeTimestampValue handles timestamp field values that might be Unix timestamps
func SanitizeTimestampValue(value interface{}) interface{} {
	switch v := value.(type) {
	case float64:
		return ConvertUnixTimestampToISO(int64(v))
	case int64:
		return ConvertUnixTimestampToISO(v)
	case int:
		return ConvertUnixTimestampToISO(int64(v))
	case json.Number:
		if intVal, err := v.Int64(); err == nil {
			return ConvertUnixTimestampToISO(intVal)
		}
		return v
	case string:
		// Check if string looks like a numeric timestamp
		if intVal, err := strconv.ParseInt(v, 10, 64); err == nil {
			return ConvertUnixTimestampToISO(intVal)
		}
		return v
	default:
		return v
	}
}

// ConvertUnixTimestampToISO converts Unix timestamps to ISO format strings
// This is a simplified version that handles common timestamp formats
func ConvertUnixTimestampToISO(timestampInt int64) any {
	// Only convert values that look like reasonable Unix timestamps
	// This avoids converting regular integers that happen to be large
	if timestampInt > 1_000_000_000 && timestampInt < 10_000_000_000_000_000 {
		var t time.Time

		// Determine timestamp precision based on magnitude
		if timestampInt > 1_000_000_000_000_000 {
			// Microsecond timestamp (16+ digits)
			t = time.UnixMicro(timestampInt)
		} else if timestampInt > 1_000_000_000_000 {
			// Millisecond timestamp (13+ digits)
			t = time.UnixMilli(timestampInt)
		} else {
			// Second timestamp (10+ digits)
			t = time.Unix(timestampInt, 0)
		}

		// Convert to ISO format
		isoTimestamp := t.UTC().Format(time.RFC3339Nano)

		return isoTimestamp
	}

	// Not a timestamp - return as is
	return timestampInt
}
