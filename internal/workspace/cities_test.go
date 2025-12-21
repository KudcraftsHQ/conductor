package workspace

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRandomCity(t *testing.T) {
	// Run multiple times to ensure it returns valid cities
	for i := 0; i < 100; i++ {
		city := RandomCity()
		assert.NotEmpty(t, city)
		assert.Contains(t, cities, city)
	}
}

func TestRandomCityExcluding_ExcludesCorrectly(t *testing.T) {
	exclude := []string{"tokyo", "paris", "london"}

	// Run multiple times
	for i := 0; i < 100; i++ {
		city := RandomCityExcluding(exclude)
		assert.NotEmpty(t, city)
		assert.NotContains(t, exclude, city)
	}
}

func TestRandomCityExcluding_EmptyExclude(t *testing.T) {
	// With empty exclude list, should return any city
	for i := 0; i < 100; i++ {
		city := RandomCityExcluding([]string{})
		assert.NotEmpty(t, city)
		assert.Contains(t, cities, city)
	}
}

func TestRandomCityExcluding_AllExcluded(t *testing.T) {
	// When all cities are excluded, it should still return a city
	allCities := AllCities()
	city := RandomCityExcluding(allCities)
	assert.NotEmpty(t, city)
	// It falls back to RandomCity when all are excluded
	assert.Contains(t, cities, city)
}

func TestAllCities(t *testing.T) {
	allCities := AllCities()

	// Check it returns a non-empty list
	assert.NotEmpty(t, allCities)

	// Check specific cities exist
	assert.Contains(t, allCities, "tokyo")
	assert.Contains(t, allCities, "paris")
	assert.Contains(t, allCities, "london")
	assert.Contains(t, allCities, "sydney")
	assert.Contains(t, allCities, "seattle")

	// Check all entries are unique
	seen := make(map[string]bool)
	for _, city := range allCities {
		assert.False(t, seen[city], "duplicate city: %s", city)
		seen[city] = true
	}
}

func TestRandomCityExcluding_LargeExcludeList(t *testing.T) {
	// Exclude most cities, leaving only a few
	allCities := AllCities()
	// Keep only the last 5 cities
	exclude := allCities[:len(allCities)-5]

	// Run multiple times
	for i := 0; i < 50; i++ {
		city := RandomCityExcluding(exclude)
		assert.NotEmpty(t, city)
		// Should only return one of the last 5 cities
		assert.Contains(t, allCities[len(allCities)-5:], city)
	}
}
