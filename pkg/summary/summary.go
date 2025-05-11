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

// Package summary provides functions to print summary sections for node resource reports.
package summary

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"kubectl-node_resources/pkg/output"      // Added for JSON types
	"kubectl-node_resources/pkg/percentiles" // Added for percentile definitions
	"kubectl-node_resources/pkg/ui"
	"kubectl-node_resources/pkg/utils"

	"k8s.io/apimachinery/pkg/api/resource"
)

// --- Functions to get summary data as structs ---

// GetResourcePercentilesData calculates and returns percentiles for a given resource.
func GetResourcePercentilesData(results []utils.NodeResult, resourceName string, summaryContext string, showEphemeralStorage bool) []output.JSONPercentileDetail { // Added showEphemeralStorage
	if len(results) == 0 {
		return []output.JSONPercentileDetail{}
	}

	sortedResults := make([]utils.NodeResult, len(results))
	copy(sortedResults, results)

	switch resourceName {
	case "CPU":
		sort.Slice(sortedResults, func(i, j int) bool {
			return sortedResults[i].CPUPercent < sortedResults[j].CPUPercent
		})
	case "Memory":
		sort.Slice(sortedResults, func(i, j int) bool {
			return sortedResults[i].MemPercent < sortedResults[j].MemPercent
		})
	case "EphemeralStorage":
		if !showEphemeralStorage { // Should not be called if false, but defensive
			return []output.JSONPercentileDetail{}
		}
		sort.Slice(sortedResults, func(i, j int) bool {
			return sortedResults[i].EphemeralStoragePercent < sortedResults[j].EphemeralStoragePercent
		})
	default:
		return []output.JSONPercentileDetail{} // Should not happen
	}

	n := len(sortedResults)
	// Use the centrally defined percentiles
	defs := percentiles.DefaultPercentiles
	jsonData := make([]output.JSONPercentileDetail, 0, len(defs))

	// Iterate in reverse to match existing print order (Max first, if DefaultPercentiles is sorted ascending by value)
	// Assuming DefaultPercentiles is sorted by value (e.g. P10, P50, P90, P99, Max)
	for i := len(defs) - 1; i >= 0; i-- {
		pDef := defs[i]
		var index int
		if pDef.Value == 1.00 { // Max value
			index = n - 1
		} else {
			index = int(float64(n-1) * pDef.Value)
		}
		if index < 0 {
			index = 0
		}
		if index >= n {
			index = n - 1
		}

		nodeRes := sortedResults[index]
		var valueString string
		var percentValue float64

		switch resourceName {
		case "CPU":
			valueString = utils.FormatCPU(nodeRes.ReqCPU)
			percentValue = nodeRes.CPUPercent
		case "Memory":
			valueString = utils.FormatMemory(nodeRes.ReqMem.Value())
			percentValue = nodeRes.MemPercent
		case "EphemeralStorage":
			valueString = utils.FormatMemory(nodeRes.ReqEphemeralStorage.Value()) // Assuming FormatMemory is suitable
			percentValue = nodeRes.EphemeralStoragePercent
		}
		jsonData = append(jsonData, output.JSONPercentileDetail{
			Percentile: pDef.Codename, // Use Codename for JSON
			NodeName:   nodeRes.Node.Name,
			Value:      valueString,
			Percent:    percentValue,
		})
	}
	// The JSON will be naturally ordered by how we append. If specific order is needed for JSON, sort jsonData here.
	// For now, it matches the print order (Max to Min percentile value).
	return jsonData
}

// GetTopHostPortsData aggregates host port usage and returns the top 10.
func GetTopHostPortsData(results []utils.NodeResult) []output.JSONHostPortSummary {
	portCounts := make(map[int32]int)
	totalPortsFound := 0
	for _, res := range results {
		for _, port := range res.HostPorts {
			portCounts[port]++
			totalPortsFound++
		}
	}

	if totalPortsFound == 0 {
		return []output.JSONHostPortSummary{}
	}

	type portStat struct {
		port  int32
		count int
	}
	var stats []portStat
	for port, count := range portCounts {
		stats = append(stats, portStat{port, count})
	}

	sort.Slice(stats, func(i, j int) bool {
		if stats[i].count != stats[j].count {
			return stats[i].count > stats[j].count
		}
		return stats[i].port < stats[j].port
	})

	limit := 10
	if len(stats) < limit {
		limit = len(stats)
	}

	jsonData := make([]output.JSONHostPortSummary, 0, limit)
	for i := 0; i < limit; i++ {
		jsonData = append(jsonData, output.JSONHostPortSummary{
			Port:      stats[i].port,
			NodeCount: stats[i].count,
		})
	}
	return jsonData
}

// GetNodeResourceSummaryData prepares the full summary data for a given command type.
func GetNodeResourceSummaryData(results []utils.NodeResult, showHostPorts bool, showEphemeralStorage bool, cmdType utils.CmdType) (*output.JSONSummary, error) {
	if len(results) == 0 {
		return nil, nil
	}

	var summaryContext string
	var totalCPUAlloc, totalMemAlloc, totalEphAlloc float64
	var totalCPUReqOrUsed, totalMemReqOrUsed, totalEphReqOrUsed float64
	var sumCPUPercent, sumMemPercent, sumEphPercent float64

	if cmdType == utils.CmdTypeAllocation {
		summaryContext = "Allocation"
	} else if cmdType == utils.CmdTypeUtilization {
		summaryContext = "Utilization"
	} else {
		return nil, fmt.Errorf("unknown command type for summary: %s", cmdType)
	}

	for _, res := range results {
		totalCPUAlloc += res.Node.Status.Allocatable.Cpu().AsApproximateFloat64()
		totalMemAlloc += float64(res.Node.Status.Allocatable.Memory().Value())
		totalCPUReqOrUsed += res.ReqCPU.AsApproximateFloat64()
		totalMemReqOrUsed += float64(res.ReqMem.Value())
		sumCPUPercent += res.CPUPercent
		sumMemPercent += res.MemPercent
		if showEphemeralStorage {
			totalEphAlloc += float64(res.AllocEphemeralStorage.Value())
			totalEphReqOrUsed += float64(res.ReqEphemeralStorage.Value())
			sumEphPercent += res.EphemeralStoragePercent
		}
	}

	avgCPUPercent := 0.0
	avgMemPercent := 0.0
	avgEphPercent := 0.0
	if len(results) > 0 {
		avgCPUPercent = sumCPUPercent / float64(len(results))
		avgMemPercent = sumMemPercent / float64(len(results))
		if showEphemeralStorage {
			avgEphPercent = sumEphPercent / float64(len(results))
		}
	}

	// TODO: Update GetResourcePercentilesData to accept showEphemeralStorage and handle ephemeral storage percentiles
	summary := &output.JSONSummary{
		TotalNodes:                 len(results),
		TotalCPUAllocatable:        utils.FormatCPU(*resource.NewMilliQuantity(int64(totalCPUAlloc*1000), resource.DecimalSI)),
		TotalCPURequestedOrUsed:    utils.FormatCPU(*resource.NewMilliQuantity(int64(totalCPUReqOrUsed*1000), resource.DecimalSI)),
		AverageCPUPercent:          avgCPUPercent,
		TotalMemoryAllocatable:     utils.FormatMemory(int64(totalMemAlloc)),
		TotalMemoryRequestedOrUsed: utils.FormatMemory(int64(totalMemReqOrUsed)),
		AverageMemoryPercent:       avgMemPercent,
		CPUPercentiles:             GetResourcePercentilesData(results, "CPU", summaryContext, showEphemeralStorage),
		MemoryPercentiles:          GetResourcePercentilesData(results, "Memory", summaryContext, showEphemeralStorage),
	}

	if showEphemeralStorage {
		summary.TotalEphemeralAllocatable = utils.FormatMemory(int64(totalEphAlloc))
		summary.TotalEphemeralRequestedOrUsed = utils.FormatMemory(int64(totalEphReqOrUsed))
		summary.AverageEphemeralPercent = avgEphPercent
		// summary.EphemeralStoragePercentiles = GetResourcePercentilesData(results, "EphemeralStorage", summaryContext, showEphemeralStorage) // Add this if JSONSummary has EphemeralStoragePercentiles
	}

	if cmdType == utils.CmdTypeAllocation && showHostPorts {
		summary.TopHostPorts = GetTopHostPortsData(results)
	}
	return summary, nil
}

// PrintNodeResourceSummary prints the summary section for resource allocation or utilization.
// It includes total nodes and percentiles for CPU, Memory, and optionally Ephemeral Storage.
// If cmdType is "allocation" and showHostPorts is true, it also prints top host port usage.
// It writes the output to the provided io.Writer (e.g., os.Stdout).
func PrintNodeResourceSummary(results []utils.NodeResult, showHostPorts bool, showEphemeralStorage bool, out io.Writer, cmdType utils.CmdType) {
	if len(results) == 0 {
		return // No data to summarize
	}

	var summaryTitle string
	var summaryContext string
	if cmdType == utils.CmdTypeAllocation {
		summaryTitle = "--- Node Resource Allocation Summary ---"
		summaryContext = "Allocation"
	} else if cmdType == utils.CmdTypeUtilization {
		summaryTitle = "--- Node Resource Utilization Summary ---"
		summaryContext = "Utilization"
	} else {
		fmt.Fprintf(out, "\n--- Unknown Node Resource Summary Type: %s ---\n", cmdType)
		return
	}

	fmt.Fprintln(out, "\n"+summaryTitle)
	fmt.Fprintf(out, "Total Nodes: %d\n", len(results))

	// TODO: Update printResourcePercentiles and printResourceDistributionHistogram signatures
	printResourcePercentiles(results, summaryContext, "CPU", out)
	printResourceDistributionHistogram(results, summaryContext, "CPU", out)
	printResourcePercentiles(results, summaryContext, "Memory", out)
	printResourceDistributionHistogram(results, summaryContext, "Memory", out)

	if showEphemeralStorage {
		printResourcePercentiles(results, summaryContext, "EphemeralStorage", out)
		printResourceDistributionHistogram(results, summaryContext, "EphemeralStorage", out)
	}

	if cmdType == utils.CmdTypeAllocation && showHostPorts {
		printTopHostPorts(results, out)
	}
}

// printResourcePercentiles calculates and prints percentiles for a given resource (CPU, Memory, or EphemeralStorage)
// based on the summary context (Allocation or Utilization).
func printResourcePercentiles(results []utils.NodeResult, summaryContext string, resourceName string, out io.Writer) { // Removed showEphemeralStorage, it's implicit if resourceName is EphemeralStorage
	// Create a copy to sort independently
	sortedResults := make([]utils.NodeResult, len(results))
	copy(sortedResults, results)

	var contextVerb string
	if summaryContext == "Allocation" {
		contextVerb = "requested"
	} else { // Utilization
		contextVerb = "used"
	}

	switch resourceName {
	case "CPU":
		sort.Slice(sortedResults, func(i, j int) bool {
			return sortedResults[i].CPUPercent < sortedResults[j].CPUPercent
		})
		fmt.Fprintf(out, "\nCPU %s Percentiles (based on %% of allocatable CPU %s):\n", summaryContext, contextVerb)
	case "Memory":
		sort.Slice(sortedResults, func(i, j int) bool {
			return sortedResults[i].MemPercent < sortedResults[j].MemPercent
		})
		fmt.Fprintf(out, "\nMemory %s Percentiles (based on %% of allocatable Memory %s):\n", summaryContext, contextVerb)
	case "EphemeralStorage":
		sort.Slice(sortedResults, func(i, j int) bool {
			return sortedResults[i].EphemeralStoragePercent < sortedResults[j].EphemeralStoragePercent
		})
		fmt.Fprintf(out, "\nEphemeral Storage %s Percentiles (based on %% of allocatable Ephemeral Storage %s):\n", summaryContext, contextVerb)
	default:
		// This case should ideally not be reached if called correctly
		fmt.Fprintf(out, "\nUnknown resource for percentiles: %s\n", resourceName)
		return
	}

	// Use the centrally defined percentiles
	defs := percentiles.DefaultPercentiles
	n := len(sortedResults)
	if n == 0 {
		fmt.Fprintln(out, "  No data available.")
		return
	}

	// Print in descending order of percentile for readability (Max first)
	// Assuming DefaultPercentiles is sorted by value (e.g. P10, P50, P90, P99, Max)
	for i := len(defs) - 1; i >= 0; i-- {
		pDef := defs[i]
		var index int
		if pDef.Value == 1.00 { // Max value
			index = n - 1
		} else {
			index = int(float64(n-1) * pDef.Value) // Standard percentile calculation (nearest rank)
		}
		if index < 0 {
			index = 0
		}
		if index >= n {
			index = n - 1
		}

		nodeRes := sortedResults[index]
		var valueString string
		var percentValue float64

		switch resourceName {
		case "CPU":
			valueString = utils.FormatCPU(nodeRes.ReqCPU)
			percentValue = nodeRes.CPUPercent
		case "Memory":
			valueString = utils.FormatMemory(nodeRes.ReqMem.Value())
			percentValue = nodeRes.MemPercent
		case "EphemeralStorage":
			valueString = utils.FormatMemory(nodeRes.ReqEphemeralStorage.Value()) // Assuming FormatMemory is suitable
			percentValue = nodeRes.EphemeralStoragePercent
		default:
			// Should not happen if resourceName validation is done prior or switch is exhaustive
			valueString = "N/A"
			percentValue = 0
		}
		// Use DisplayName for text output, ensure sufficient padding for potentially longer names
		// Removed the per-percentile bar chart from here
		fmt.Fprintf(out, "  - %-15s: %s (%s: %s, %s%.1f%%%s)\n",
			pDef.DisplayName, nodeRes.Node.Name, strings.Title(contextVerb), valueString,
			ui.PercentFontColor(percentValue), percentValue, ui.ColorReset)
	}
}

// printResourceDistributionHistogram prints a histogram of node distribution across utilization/allocation buckets.
func printResourceDistributionHistogram(results []utils.NodeResult, summaryContext string, resourceName string, out io.Writer) { // Removed showEphemeralStorage for consistency
	if len(results) == 0 {
		return
	}

	var contextVerb string
	if summaryContext == "Allocation" {
		contextVerb = "requested"
	} else { // Utilization
		contextVerb = "used"
	}

	fmt.Fprintf(out, "\n%s %s Distribution (%% of allocatable %s %s):\n", resourceName, summaryContext, resourceName, contextVerb)

	// Define buckets (0-10, 10-20, ..., 90-100)
	numBuckets := 10
	buckets := make([]int, numBuckets)
	bucketSize := 10.0 // Each bucket represents 10%

	for _, res := range results {
		var percentValue float64
		switch resourceName {
		case "CPU":
			percentValue = res.CPUPercent
		case "Memory":
			percentValue = res.MemPercent
		case "EphemeralStorage":
			percentValue = res.EphemeralStoragePercent
		default:
			// Should not happen if called correctly
			continue
		}

		bucketIndex := int(math.Floor(percentValue / bucketSize))
		if bucketIndex >= numBuckets { // Handle 100% case
			bucketIndex = numBuckets - 1
		}
		if bucketIndex < 0 { // Should not happen if percentValue is >= 0
			bucketIndex = 0
		}
		buckets[bucketIndex]++
	}

	maxNodesInBucket := 0
	for _, count := range buckets {
		if count > maxNodesInBucket {
			maxNodesInBucket = count
		}
	}

	// Max bar width for the histogram
	maxBarWidth := 40
	if maxNodesInBucket == 0 { // Avoid division by zero if all buckets are empty (though results shouldn't be empty here)
		maxBarWidth = 1 // or handle as "no data"
	}

	for i := 0; i < numBuckets; i++ {
		lowerBound := i * int(bucketSize)
		upperBound := (i + 1) * int(bucketSize)
		label := fmt.Sprintf("%2d-%3d%%", lowerBound, upperBound)
		if i == numBuckets-1 { // Last bucket is inclusive of 100%
			label = fmt.Sprintf("%2d-%3d%%", lowerBound, 100)
		}

		nodeCount := buckets[i]
		barLength := 0
		if maxNodesInBucket > 0 { // Calculate bar length only if there's data
			barLength = int(math.Round((float64(nodeCount) / float64(maxNodesInBucket)) * float64(maxBarWidth)))
		}

		bar := strings.Repeat("█", barLength)

		// Determine color based on the midpoint of the bucket for representative coloring
		bucketMidPointPercent := float64(lowerBound) + bucketSize/2.0
		color := ui.PercentFontColor(bucketMidPointPercent)

		fmt.Fprintf(out, "  %s : %s%s%s (%d nodes)\n", label, color, bar, ui.ColorReset, nodeCount)
	}
}

// printTopHostPorts aggregates host port usage and prints the top 10.
// This is only relevant for the 'allocation' command.
func printTopHostPorts(results []utils.NodeResult, out io.Writer) {
	fmt.Fprintf(out, "\nTop 10 Host Ports by Node Usage:\n")

	portCounts := make(map[int32]int)
	totalPortsFound := 0
	for _, res := range results {
		for _, port := range res.HostPorts {
			portCounts[port]++
			totalPortsFound++
		}
	}

	if totalPortsFound == 0 {
		fmt.Fprintln(out, "  No host ports in use across selected nodes.")
		return
	}

	type portStat struct {
		port  int32
		count int
	}
	var stats []portStat
	for port, count := range portCounts {
		stats = append(stats, portStat{port, count})
	}

	sort.Slice(stats, func(i, j int) bool {
		if stats[i].count != stats[j].count {
			return stats[i].count > stats[j].count // Sort by count descending
		}
		return stats[i].port < stats[j].port // Then by port number ascending
	})

	limit := 10
	if len(stats) < limit {
		limit = len(stats)
	}

	if limit == 0 { // Should be caught by totalPortsFound == 0, but as a safeguard
		fmt.Fprintln(out, "  No host ports in use across selected nodes.")
		return
	}

	for i := 0; i < limit; i++ {
		fmt.Fprintf(out, "  - Port %-5d: Used on %d nodes\n", stats[i].port, stats[i].count)
	}
	if len(stats) > limit {
		fmt.Fprintf(out, "  ... and %d more ports.\n", len(stats)-limit)
	}
}
