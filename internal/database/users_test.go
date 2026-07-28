package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateSecurePassword(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{"short password", 8},
		{"medium password", 16},
		{"long password", 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			password := GenerateSecurePassword(tt.length)
			assert.Len(t, password, tt.length)

			// Verify all characters are alphanumeric
			for _, c := range password {
				isAlphaNum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
				assert.True(t, isAlphaNum, "character %c should be alphanumeric", c)
			}
		})
	}

	// Test that passwords are random (different)
	t.Run("passwords are unique", func(t *testing.T) {
		p1 := GenerateSecurePassword(32)
		p2 := GenerateSecurePassword(32)
		assert.NotEqual(t, p1, p2)
	})
}
