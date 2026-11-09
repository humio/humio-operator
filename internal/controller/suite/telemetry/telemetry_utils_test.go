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

package telemetry

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/humio/humio-operator/internal/humio"
)

var _ = Describe("Telemetry Utilities", Label("envtest", "dummy", "real"), func() {

	Context("Timestamp Sanitization", func() {

		Describe("sanitizeTimestamps function", func() {
			It("should leave regular integers unchanged", func() {
				input := int64(12345)
				result := humio.SanitizeTimestamps(input)
				Expect(result).To(Equal(int64(12345)))
			})

			It("should leave microsecond timestamps unchanged at top level", func() {
				// Large numbers are no longer automatically converted
				input := int64(1766072498008000) // 16 digits - microseconds
				result := humio.SanitizeTimestamps(input)
				Expect(result).To(Equal(int64(1766072498008000)))
			})

			It("should convert known timestamp fields to ISO format", func() {
				input := map[string]interface{}{
					"name":               "test",
					"last_activity_time": int64(1766072498), // seconds - known timestamp field
					"value":              42,
				}

				expected := map[string]interface{}{
					"name":               "test",
					"last_activity_time": "2025-12-18T15:41:38Z", // converted to ISO format
					"value":              42,
				}

				result := humio.SanitizeTimestamps(input)
				Expect(result).To(Equal(expected))
			})

			It("should leave regular timestamp fields unchanged", func() {
				input := map[string]interface{}{
					"name":      "test",
					"timestamp": int64(1766072498008000), // No longer a known timestamp field
					"value":     42,
				}

				expected := map[string]interface{}{
					"name":      "test",
					"timestamp": int64(1766072498008000), // Unchanged
					"value":     42,
				}

				result := humio.SanitizeTimestamps(input)
				Expect(result).To(Equal(expected))
			})

			It("should not automatically convert timestamps in arrays", func() {
				input := []interface{}{
					int64(1766072498008000), // microseconds - no longer converted
					"regular string",
					int64(1766072498008), // milliseconds - no longer converted
				}

				expected := []interface{}{
					int64(1766072498008000), // unchanged
					"regular string",
					int64(1766072498008), // unchanged
				}

				result := humio.SanitizeTimestamps(input)
				Expect(result).To(Equal(expected))
			})

			It("should handle nested structures correctly", func() {
				input := map[string]interface{}{
					"metadata": map[string]interface{}{
						"last_activity_time": int64(1766072498), // Known timestamp field
						"regular_field":      "value",
					},
					"data": []interface{}{
						map[string]interface{}{
							"event_time": int64(1766072500), // Another known timestamp field
							"message":    "test event",
						},
					},
				}

				expected := map[string]interface{}{
					"metadata": map[string]interface{}{
						"last_activity_time": "2025-12-18T15:41:38Z", // Converted
						"regular_field":      "value",
					},
					"data": []interface{}{
						map[string]interface{}{
							"event_time": "2025-12-18T15:41:40Z", // Converted
							"message":    "test event",
						},
					},
				}

				result := humio.SanitizeTimestamps(input)
				Expect(result).To(Equal(expected))
			})
		})

		Describe("convertUnixTimestampToISO function", func() {
			It("should convert 16 digit microsecond timestamps", func() {
				input := int64(1766072498008000)
				result := humio.ConvertUnixTimestampToISO(input)
				Expect(result).To(Equal("2025-12-18T15:41:38.008Z"))
			})

			It("should convert 13 digit millisecond timestamps", func() {
				input := int64(1766072498008)
				result := humio.ConvertUnixTimestampToISO(input)
				Expect(result).To(Equal("2025-12-18T15:41:38.008Z"))
			})

			It("should convert 10 digit second timestamps", func() {
				input := int64(1766072498)
				result := humio.ConvertUnixTimestampToISO(input)
				Expect(result).To(Equal("2025-12-18T15:41:38Z"))
			})

			It("should leave small numbers unchanged", func() {
				input := int64(12345)
				result := humio.ConvertUnixTimestampToISO(input)
				Expect(result).To(Equal(int64(12345)))
			})

			It("should leave very large numbers unchanged", func() {
				// Beyond reasonable timestamp range
				input := int64(17660724980080001)
				result := humio.ConvertUnixTimestampToISO(input)
				Expect(result).To(Equal(int64(17660724980080001)))
			})

			It("should handle edge cases properly", func() {
				// Test boundary conditions
				By("Testing just above minimum threshold")
				result1 := humio.ConvertUnixTimestampToISO(1000000001) // Just above 1 billion threshold
				Expect(result1).To(Equal("2001-09-09T01:46:41Z"))

				By("Testing at minimum threshold (should not convert)")
				result2 := humio.ConvertUnixTimestampToISO(1000000000) // Exactly at boundary - should NOT convert
				Expect(result2).To(Equal(int64(1000000000)))

				By("Testing just below minimum threshold")
				result3 := humio.ConvertUnixTimestampToISO(999999999) // Just below boundary
				Expect(result3).To(Equal(int64(999999999)))

				By("Testing maximum reasonable timestamp")
				result4 := humio.ConvertUnixTimestampToISO(9999999999999999) // Just under our max
				Expect(result4).To(Equal("2286-11-20T17:46:39.999999Z"))

				By("Testing at maximum threshold (should not convert)")
				result5 := humio.ConvertUnixTimestampToISO(10000000000000000) // Exactly at upper boundary - should NOT convert
				Expect(result5).To(Equal(int64(10000000000000000)))
			})
		})

		Describe("isTimestampField function", func() {
			It("should identify known timestamp fields", func() {
				knownFields := []string{
					"last_activity_time",
					"event_time",
					"created_at",
					"updated_at",
				}

				for _, field := range knownFields {
					result := humio.IsTimestampField(field)
					Expect(result).To(BeTrue(), "Field %s should be recognized as timestamp field", field)
				}
			})

			It("should not identify regular fields as timestamps", func() {
				regularFields := []string{
					"@timestamp", // Handled by struct JSON tags, not sanitization
					"timestamp",  // Regular timestamp field, not automatically converted
					"name",
					"value",
					"id",
					"data",
				}

				for _, field := range regularFields {
					result := humio.IsTimestampField(field)
					Expect(result).To(BeFalse(), "Field %s should not be recognized as timestamp field", field)
				}
			})
		})

		Describe("sanitizeTimestampValue function", func() {
			It("should handle different numeric types", func() {
				timestamp := int64(1766072498) // 10 digit seconds
				expectedISO := "2025-12-18T15:41:38Z"

				By("Testing int64")
				result1 := humio.SanitizeTimestampValue(timestamp)
				Expect(result1).To(Equal(expectedISO))

				By("Testing int")
				result2 := humio.SanitizeTimestampValue(int(timestamp))
				Expect(result2).To(Equal(expectedISO))

				By("Testing float64")
				result3 := humio.SanitizeTimestampValue(float64(timestamp))
				Expect(result3).To(Equal(expectedISO))
			})

			It("should handle json.Number type", func() {
				jsonNum := json.Number("1766072498")
				result := humio.SanitizeTimestampValue(jsonNum)
				Expect(result).To(Equal("2025-12-18T15:41:38Z"))
			})

			It("should handle string representations of timestamps", func() {
				stringTimestamp := "1766072498"
				result := humio.SanitizeTimestampValue(stringTimestamp)
				Expect(result).To(Equal("2025-12-18T15:41:38Z"))
			})

			It("should leave non-timestamp strings unchanged", func() {
				regularString := "not-a-timestamp"
				result := humio.SanitizeTimestampValue(regularString)
				Expect(result).To(Equal("not-a-timestamp"))
			})

			It("should leave other types unchanged", func() {
				By("Testing boolean")
				result1 := humio.SanitizeTimestampValue(true)
				Expect(result1).To(BeTrue())

				By("Testing nil")
				result2 := humio.SanitizeTimestampValue(nil)
				Expect(result2).To(BeNil())
			})
		})
	})

	Context("Integration with Export Flow", func() {
		It("should work correctly with telemetry export sanitization", func() {
			// This tests the integration with the actual export flow
			input := map[string]interface{}{
				"@timestamp":      "2025-12-18T15:41:38.008Z", // Already ISO, should remain unchanged
				"cluster_id":      "test-cluster",
				"collection_type": "repository_usage",
				"source_type":     "json",
				"data": map[string]interface{}{
					"name":               "test-repo",
					"last_activity_time": int64(1766072498), // Should be converted
					"regular_number":     int64(12345),      // Should remain unchanged
				},
			}

			result := humio.SanitizeTimestamps(input)

			// Verify structure is preserved
			resultMap, ok := result.(map[string]interface{})
			Expect(ok).To(BeTrue())

			// Verify @timestamp remains unchanged
			Expect(resultMap["@timestamp"]).To(Equal("2025-12-18T15:41:38.008Z"))

			// Verify other fields remain unchanged
			Expect(resultMap["cluster_id"]).To(Equal("test-cluster"))
			Expect(resultMap["collection_type"]).To(Equal("repository_usage"))

			// Verify data field processing
			dataMap, ok := resultMap["data"].(map[string]interface{})
			Expect(ok).To(BeTrue())
			Expect(dataMap["name"]).To(Equal("test-repo"))
			Expect(dataMap["last_activity_time"]).To(Equal("2025-12-18T15:41:38Z")) // Converted
			Expect(dataMap["regular_number"]).To(Equal(int64(12345)))               // Unchanged
		})
	})
})
