package batch

import (
	"fmt"
	"testing"
)

// TestValidateMetadata tests the ValidateMetadata function
func TestValidateMetadata(t *testing.T) {
	t.Run("ValidMetadata", func(t *testing.T) {
		metadata := map[string]string{
			"key1": "value1",
			"key2": "value2",
		}

		err := ValidateMetadata(metadata)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
	})

	t.Run("TooManyKeyValuePairs", func(t *testing.T) {
		metadata := make(map[string]string)
		// Create 17 key-value pairs (exceeds limit of 16)
		for i := 0; i < 17; i++ {
			metadata[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value%d", i)
		}

		err := ValidateMetadata(metadata)
		if err == nil {
			t.Error("Expected error for too many key-value pairs, got nil")
		}

		// Check that it's the right type of error
		if _, ok := err.(*MetadataValidationError); !ok {
			t.Errorf("Expected MetadataValidationError, got %T", err)
		}
	})

	t.Run("KeyTooLong", func(t *testing.T) {
		// Create a key that's too long (exceeds 64 characters)
		longKey := make([]byte, 65)
		for i := range longKey {
			longKey[i] = 'a'
		}

		metadata := map[string]string{
			string(longKey): "value",
		}

		err := ValidateMetadata(metadata)
		if err == nil {
			t.Error("Expected error for key too long, got nil")
		}

		// Check that it's the right type of error
		if _, ok := err.(*MetadataValidationError); !ok {
			t.Errorf("Expected MetadataValidationError, got %T", err)
		}
	})

	t.Run("ValueTooLong", func(t *testing.T) {
		// Create a value that's too long (exceeds 512 characters)
		longValue := make([]byte, 513)
		for i := range longValue {
			longValue[i] = 'a'
		}

		metadata := map[string]string{
			"key": string(longValue),
		}

		err := ValidateMetadata(metadata)
		if err == nil {
			t.Error("Expected error for value too long, got nil")
		}

		// Check that it's the right type of error
		if _, ok := err.(*MetadataValidationError); !ok {
			t.Errorf("Expected MetadataValidationError, got %T", err)
		}
	})

	t.Run("EmptyMetadata", func(t *testing.T) {
		metadata := map[string]string{}

		err := ValidateMetadata(metadata)
		if err != nil {
			t.Errorf("Expected no error for empty metadata, got %v", err)
		}
	})

	t.Run("NilMetadata", func(t *testing.T) {
		var metadata map[string]string

		err := ValidateMetadata(metadata)
		if err != nil {
			t.Errorf("Expected no error for nil metadata, got %v", err)
		}
	})
}
