package controller

import (
	"testing"
)

func TestNormalizeKafkaProperties(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name:     "sorted properties",
			input:    "bootstrap.servers=kafka:9092\nsecurity.protocol=SASL_SSL",
			expected: "bootstrap.servers=kafka:9092\nsecurity.protocol=SASL_SSL",
			wantErr:  false,
		},
		{
			name:     "unsorted properties",
			input:    "security.protocol=SASL_SSL\nbootstrap.servers=kafka:9092",
			expected: "bootstrap.servers=kafka:9092\nsecurity.protocol=SASL_SSL",
			wantErr:  false,
		},
		{
			name:     "properties with whitespace around equals",
			input:    "bootstrap.servers = kafka:9092\nsecurity.protocol= SASL_SSL",
			expected: "bootstrap.servers=kafka:9092\nsecurity.protocol=SASL_SSL",
			wantErr:  false,
		},
		{
			name:     "properties with leading/trailing whitespace",
			input:    "  bootstrap.servers=kafka:9092  \n  security.protocol=SASL_SSL  ",
			expected: "bootstrap.servers=kafka:9092\nsecurity.protocol=SASL_SSL",
			wantErr:  false,
		},
		{
			name:     "properties with comments and blank lines",
			input:    "bootstrap.servers=kafka:9092\n# Comment line\n\nsecurity.protocol=SASL_SSL",
			expected: "bootstrap.servers=kafka:9092\nsecurity.protocol=SASL_SSL",
			wantErr:  false,
		},
		{
			name:     "properties with Windows line endings",
			input:    "bootstrap.servers=kafka:9092\r\nsecurity.protocol=SASL_SSL",
			expected: "bootstrap.servers=kafka:9092\nsecurity.protocol=SASL_SSL",
			wantErr:  false,
		},
		{
			name:     "properties with trailing newline",
			input:    "bootstrap.servers=kafka:9092\nsecurity.protocol=SASL_SSL\n",
			expected: "bootstrap.servers=kafka:9092\nsecurity.protocol=SASL_SSL",
			wantErr:  false,
		},
		{
			name:     "properties with multiple trailing newlines",
			input:    "bootstrap.servers=kafka:9092\nsecurity.protocol=SASL_SSL\n\n\n",
			expected: "bootstrap.servers=kafka:9092\nsecurity.protocol=SASL_SSL",
			wantErr:  false,
		},
		{
			name:     "empty properties",
			input:    "",
			expected: "",
			wantErr:  false,
		},
		{
			name:     "only whitespace",
			input:    "   \n  \n  ",
			expected: "",
			wantErr:  false,
		},
		{
			name:     "only comments",
			input:    "# Comment 1\n# Comment 2",
			expected: "",
			wantErr:  false,
		},
		{
			name:     "duplicate keys - last wins",
			input:    "bootstrap.servers=kafka1:9092\nbootstrap.servers=kafka2:9092",
			expected: "bootstrap.servers=kafka2:9092",
			wantErr:  false,
		},
		{
			name:     "invalid format - no equals",
			input:    "invalid-no-equals",
			expected: "",
			wantErr:  true,
		},
		{
			name:     "invalid format - empty key",
			input:    "=value",
			expected: "",
			wantErr:  true,
		},
		{
			name:     "property value with spaces",
			input:    "sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username=\"user\" password=\"pass\"",
			expected: "sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username=\"user\" password=\"pass\"",
			wantErr:  false,
		},
		{
			name:     "property value with equals signs",
			input:    "sasl.jaas.config=key1=value1 key2=value2",
			expected: "sasl.jaas.config=key1=value1 key2=value2",
			wantErr:  false,
		},
		{
			name: "complex real-world example",
			input: `# Kafka broker configuration
bootstrap.servers=kafka1:9092,kafka2:9092,kafka3:9092

# Security configuration
security.protocol=SASL_SSL
sasl.mechanism=PLAIN
sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username="user" password="secret"

# Performance tuning
compression.type=snappy
batch.size=16384
linger.ms=10

# Reliability
acks=all
retries=3`,
			expected: "acks=all\nbatch.size=16384\nbootstrap.servers=kafka1:9092,kafka2:9092,kafka3:9092\ncompression.type=snappy\nlinger.ms=10\nretries=3\nsasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username=\"user\" password=\"secret\"\nsasl.mechanism=PLAIN\nsecurity.protocol=SASL_SSL",
			wantErr:  false,
		},
		{
			name:     "mixed line endings",
			input:    "key1=value1\r\nkey2=value2\nkey3=value3",
			expected: "key1=value1\nkey2=value2\nkey3=value3",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeKafkaProperties(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("normalizeKafkaProperties() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("normalizeKafkaProperties() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// TestNormalizeKafkaProperties_Idempotent verifies that normalization is idempotent
func TestNormalizeKafkaProperties_Idempotent(t *testing.T) {
	testCases := []string{
		"bootstrap.servers=kafka:9092\nsecurity.protocol=SASL_SSL",
		"key1=value1\nkey2=value2\nkey3=value3",
		"",
	}

	for _, input := range testCases {
		normalized1, err := normalizeKafkaProperties(input)
		if err != nil {
			t.Fatalf("First normalization failed: %v", err)
		}

		normalized2, err := normalizeKafkaProperties(normalized1)
		if err != nil {
			t.Fatalf("Second normalization failed: %v", err)
		}

		if normalized1 != normalized2 {
			t.Errorf("Normalization is not idempotent:\nFirst:  %q\nSecond: %q", normalized1, normalized2)
		}
	}
}
