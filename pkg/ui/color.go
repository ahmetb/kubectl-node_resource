// Copyright 2025 Ahmet Alp Balkan
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package ui provides utilities for user interface elements, such as terminal colors and progress bars.
package ui

import (
	"fmt"
	"math"
)

// ColorReset is the ANSI escape code to reset terminal color.
const ColorReset = "\033[0m"

// cubeLevels are the standard values for each component in the 6x6x6 color cube.
var cubeLevels = []int{0, 95, 135, 175, 215, 255}

// interpolateComponent calculates a single color component's value along a linear gradient.
func interpolateComponent(start, end float64, factor float64) int {
	val := start + (end-start)*factor
	// Clamp the value to be within 0-255
	return int(math.Max(0, math.Min(255, math.Round(val))))
}

// findClosestCubeIndex finds the index (0-5) in cubeLevels that is closest to the given value.
func findClosestCubeIndex(value int) int {
	closestIndex := 0
	minDiff := math.Abs(float64(value - cubeLevels[0]))

	for i := 1; i < len(cubeLevels); i++ {
		diff := math.Abs(float64(value - cubeLevels[i]))
		if diff < minDiff {
			minDiff = diff
			closestIndex = i
		}
	}
	return closestIndex
}

// rgbTo256ColorIndex converts an R, G, B (0-255) triplet to its closest
// equivalent in the terminal's 256-color palette (specifically, the 6x6x6 color cube).
func rgbTo256ColorIndex(r, g, b int) int {
	rIdx := findClosestCubeIndex(r)
	gIdx := findClosestCubeIndex(g)
	bIdx := findClosestCubeIndex(b)

	// Formula for 6x6x6 color cube (indices 16-231)
	return 16 + (rIdx * 36) + (gIdx * 6) + bIdx
}

// PercentFontColor returns an ANSI escape code string for a foreground color
// representing the given percentage on a green-yellow-red gradient.
// Percentages are clamped to the 0-100 range.
// 0-50% transitions from green to yellow.
// 50-100% transitions from yellow to red.
func PercentFontColor(percentage float64) string {
	// Clamp percentage to the 0-100 range
	p := math.Max(0, math.Min(100, percentage))

	var r, g, b int

	if p <= 50 {
		// Gradient from Green (0,255,0) to Yellow (255,255,0)
		factor := p / 50.0
		r = interpolateComponent(0, 255, factor) // Red component increases
		g = 255                                  // Green component stays max
		b = 0                                    // Blue component stays min
	} else {
		// Gradient from Yellow (255,255,0) to Red (255,0,0)
		factor := (p - 50.0) / 50.0
		r = 255                                  // Red component stays max
		g = interpolateComponent(255, 0, factor) // Green component decreases
		b = 0                                    // Blue component stays min
	}

	colorIndex := rgbTo256ColorIndex(r, g, b)
	return fmt.Sprintf("\033[38;5;%dm", colorIndex)
}

// PercentBackgroundColor returns an ANSI escape code string for a background color
// representing the given percentage on a green-yellow-red gradient.
// Percentages are clamped to the 0-100 range.
// 0-50% transitions from green to yellow.
// 50-100% transitions from yellow to red.
func PercentBackgroundColor(percentage float64) string {
	// Clamp percentage to the 0-100 range
	p := math.Max(0, math.Min(100, percentage))

	var r, g, b int

	// use black color for foreground for light backgrounds
	var foregroundColor string
	if percentage < 65 {
		foregroundColor = "\033[30m" // black font
	}

	if p <= 50 {
		// Gradient from Green (0,255,0) to Yellow (255,255,0)
		factor := p / 50.0
		r = interpolateComponent(0, 255, factor) // Red component increases
		g = 255                                  // Green component stays max
		b = 0                                    // Blue component stays min
	} else {
		// Gradient from Yellow (255,255,0) to Red (255,0,0)
		factor := (p - 50.0) / 50.0
		r = 255                                  // Red component stays max
		g = interpolateComponent(255, 0, factor) // Green component decreases
		b = 0                                    // Blue component stays min
	}

	colorIndex := rgbTo256ColorIndex(r, g, b)
	return fmt.Sprintf(foregroundColor+"\033[48;5;%dm", colorIndex)
}
