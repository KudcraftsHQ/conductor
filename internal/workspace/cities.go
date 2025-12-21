package workspace

import (
	"math/rand"
)

var cities = []string{
	"tokyo", "paris", "london", "rome", "berlin",
	"madrid", "lisbon", "vienna", "prague", "amsterdam",
	"oslo", "stockholm", "helsinki", "dublin", "brussels",
	"zurich", "geneva", "milan", "barcelona", "munich",
	"warsaw", "budapest", "athens", "istanbul", "cairo",
	"dubai", "mumbai", "delhi", "bangkok", "singapore",
	"seoul", "taipei", "sydney", "melbourne", "auckland",
	"vancouver", "toronto", "montreal", "boston", "seattle",
	"denver", "austin", "miami", "chicago", "portland",
	"phoenix", "dallas", "atlanta", "detroit", "nashville",
	"perth", "brisbane", "osaka", "kyoto", "shanghai",
	"beijing", "hongkong", "manila", "jakarta", "hanoi",
	"krakow", "naples", "florence", "venice", "porto",
	"seville", "valencia", "lyon", "marseille", "bordeaux",
	"edinburgh", "glasgow", "belfast", "cardiff", "bristol",
	"oxford", "cambridge", "leeds", "manchester", "liverpool",
}

// RandomCity returns a random city name
func RandomCity() string {
	return cities[rand.Intn(len(cities))]
}

// RandomCityExcluding returns a random city not in the exclude list
func RandomCityExcluding(exclude []string) string {
	excludeSet := make(map[string]bool)
	for _, c := range exclude {
		excludeSet[c] = true
	}

	available := make([]string, 0)
	for _, c := range cities {
		if !excludeSet[c] {
			available = append(available, c)
		}
	}

	if len(available) == 0 {
		// All cities used, just return a random one
		return RandomCity()
	}

	return available[rand.Intn(len(available))]
}

// AllCities returns the list of all available city names
func AllCities() []string {
	return cities
}
