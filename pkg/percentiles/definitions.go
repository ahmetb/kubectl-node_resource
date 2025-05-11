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

package percentiles

// PercentileDefinition holds the configuration for a percentile.
type PercentileDefinition struct {
	Codename    string  // e.g., "p50", "p99", "max" - for JSON field names/values
	DisplayName string  // e.g., "P50 (Median)", "P99", "Max (P100)" - for human-readable output
	Value       float64 // The percentile value, e.g., 0.50 for 50th percentile, 1.00 for 100th (max)
}

// DefaultPercentiles defines the standard set of percentiles used in summaries.
// Ordered by value for potential programmatic iteration, though display order might vary.
var DefaultPercentiles = []PercentileDefinition{
	{Codename: "p0", DisplayName: "P0 (Min)", Value: 0.0},
	{Codename: "p10", DisplayName: "P10", Value: 0.10},
	{Codename: "p50", DisplayName: "P50 (Median)", Value: 0.50},
	{Codename: "p90", DisplayName: "P90", Value: 0.90},
	{Codename: "p99", DisplayName: "P99", Value: 0.99},
	{Codename: "p100", DisplayName: "P100 (Max)", Value: 1.00},
}
