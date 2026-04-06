package vector

import (
	"math"
)

// EuclideanDistance calculates the Euclidean distance between two vectors.
func EuclideanDistance(a, b []float64) float64 {
	if len(a) != len(b) {
		return math.NaN() // Return NaN if vectors are of different dimensions
	}
	sum := 0.0
	for i := range a {
		diff := a[i] - b[i]
		sum += diff * diff
	}
	return math.Sqrt(sum)
}

// CosineDistance calculates the cosine distance between two vectors.
func CosineDistance(a, b []float64) float64 {
	if len(a) != len(b) {
		return math.NaN() // Return NaN if vectors are of different dimensions
	}
	dotProduct := 0.0
	magnitudeA := 0.0
	magnitudeB := 0.0
	for i := range a {
		dotProduct += a[i] * b[i]
		magnitudeA += a[i] * a[i]
		magnitudeB += b[i] * b[i]
	}
	if magnitudeA == 0 || magnitudeB == 0 {
		return 1.0 // Return distance of 1 if either vector is zero
	}
	return 1 - (dotProduct / (math.Sqrt(magnitudeA) * math.Sqrt(magnitudeB)))
}