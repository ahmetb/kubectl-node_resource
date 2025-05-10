package main

import (
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

// printAllocationSummary prints the summary section for resource allocations.
// This function is called by the 'allocations' command.
func printAllocationSummary(results []nodeResult, showHostPorts bool) {
	if len(results) == 0 {
		return // No data to summarize
	}

	fmt.Println("\n--- Node Resource Allocation Summary ---")
	fmt.Printf("Total Nodes: %d\n", len(results))

	printResourcePercentiles(results, "Allocation", "CPU")    // Corrected call
	printResourcePercentiles(results, "Allocation", "Memory") // Corrected call

	if showHostPorts {
		printTopHostPorts(results)
	}
}

// printUtilizationSummary prints the summary section for resource utilization.
// This function is called by the 'utilization' command.
func printUtilizationSummary(results []nodeResult) {
	if len(results) == 0 {
		return // No data to summarize
	}

	fmt.Println("\n--- Node Resource Utilization Summary ---")
	fmt.Printf("Total Nodes: %d\n", len(results))

	printResourcePercentiles(results, "Utilization", "CPU")    // Corrected call
	printResourcePercentiles(results, "Utilization", "Memory") // Corrected call
}

// printResourcePercentiles calculates and prints percentiles for a given resource (CPU or Memory)
// based on the summary context (Allocation or Utilization).
func printResourcePercentiles(results []nodeResult, summaryContext string, resourceName string) { // Definition is correct
	// Create a copy to sort independently
	sortedResults := make([]nodeResult, len(results))
	copy(sortedResults, results)

	var contextVerb string
	if summaryContext == "Allocation" {
		contextVerb = "requested"
	} else { // Utilization
		contextVerb = "used"
	}

	if resourceName == "CPU" {
		sort.Slice(sortedResults, func(i, j int) bool {
			return sortedResults[i].cpuPercent < sortedResults[j].cpuPercent
		})
		fmt.Printf("\nCPU %s Percentiles (based on %% of allocatable CPU %s):\n", summaryContext, contextVerb)
	} else { // Memory
		sort.Slice(sortedResults, func(i, j int) bool {
			return sortedResults[i].memPercent < sortedResults[j].memPercent
		})
		fmt.Printf("\nMemory %s Percentiles (based on %% of allocatable Memory %s):\n", summaryContext, contextVerb)
	}

	percentiles := []struct {
		name  string
		value float64
	}{
		{"P10", 0.10},
		{"P50 (median)", 0.50},
		{"P90", 0.90},
		{"P100 (max)", 1.00}, // Max is effectively P100
	}

	n := len(sortedResults)
	if n == 0 {
		fmt.Println("  No data available.")
		return
	}

	// Print in descending order of percentile for readability (Max first)
	for i := len(percentiles) - 1; i >= 0; i-- {
		p := percentiles[i]
		var index int
		if p.value == 1.00 { // Max
			index = n - 1
		} else {
			index = int(float64(n-1) * p.value) // Standard percentile calculation (nearest rank)
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

		if resourceName == "CPU" {
			valueString = formatCPU(nodeRes.reqCPU) // reqCPU stores actual usage in utilization context
			percentValue = nodeRes.cpuPercent
		} else { // Memory
			valueString = formatMemory(nodeRes.reqMem.Value()) // reqMem stores actual usage in utilization context
			percentValue = nodeRes.memPercent
		}
		fmt.Printf("  - %-12s: %s (%s: %s, %.1f%%)\n",
			p.name, nodeRes.node.Name, strings.Title(contextVerb), valueString, percentValue)
	}
}

// printTopHostPorts aggregates host port usage and prints the top 10.
// This is only relevant for the 'allocations' command.
func printTopHostPorts(results []nodeResult) {
	fmt.Printf("\nTop 10 Host Ports by Node Usage:\n")

	portCounts := make(map[int32]int)
	totalPortsFound := 0
	for _, res := range results {
		for _, port := range res.hostPorts {
			portCounts[port]++
			totalPortsFound++
		}
	}

	if totalPortsFound == 0 {
		fmt.Println("  No host ports in use across selected nodes.")
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
		fmt.Println("  No host ports in use across selected nodes.")
		return
	}

	for i := 0; i < limit; i++ {
		fmt.Printf("  - Port %-5d: Used on %d nodes\n", stats[i].port, stats[i].count)
	}
	if len(stats) > limit {
		fmt.Printf("  ... and %d more ports.\n", len(stats)-limit)
	}
}

// formatCPU and formatMemory are utility functions used by summaries.
// They are also used in main.go for table printing, so they are duplicated here
// for now to keep summary_utils.go self-contained in terms of formatting.
// Ideally, these could be in a shared util package if the project grows.

func formatCPU(q resource.Quantity) string {
	cores := q.AsApproximateFloat64()
	return fmt.Sprintf("%.1f", cores)
}

func formatMemory(bytes int64) string {
	const (
		mib = 1024 * 1024
		gib = mib * 1024
	)
	switch {
	case bytes >= gib:
		return fmt.Sprintf("%.1fG", float64(bytes)/float64(gib))
	default:
		return fmt.Sprintf("%dM", bytes/mib)
	}
}
