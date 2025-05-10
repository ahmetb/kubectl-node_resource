// Package summary provides functions to print summary sections for node resource reports.
package summary

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"kubectl-node_resources/pkg/ui"
	"kubectl-node_resources/pkg/utils"
)

// PrintAllocationSummary prints the summary section for resource allocations.
// It includes total nodes and percentiles for CPU and Memory allocations.
// If showHostPorts is true, it also prints top host port usage.
// It writes the output to the provided io.Writer (e.g., os.Stdout).
func PrintAllocationSummary(results []utils.NodeResult, showHostPorts bool, out io.Writer) {
	if len(results) == 0 {
		return // No data to summarize
	}

	fmt.Fprintln(out, "\n--- Node Resource Allocation Summary ---")
	fmt.Fprintf(out, "Total Nodes: %d\n", len(results))

	printResourcePercentiles(results, "Allocation", "CPU", out)
	printResourcePercentiles(results, "Allocation", "Memory", out)

	if showHostPorts {
		printTopHostPorts(results, out)
	}
}

// PrintUtilizationSummary prints the summary section for resource utilization.
// It includes total nodes and percentiles for CPU and Memory utilization.
// It writes the output to the provided io.Writer (e.g., os.Stdout).
func PrintUtilizationSummary(results []utils.NodeResult, out io.Writer) {
	if len(results) == 0 {
		return // No data to summarize
	}

	fmt.Fprintln(out, "\n--- Node Resource Utilization Summary ---")
	fmt.Fprintf(out, "Total Nodes: %d\n", len(results))

	printResourcePercentiles(results, "Utilization", "CPU", out)
	printResourcePercentiles(results, "Utilization", "Memory", out)
}

// printResourcePercentiles calculates and prints percentiles for a given resource (CPU or Memory)
// based on the summary context (Allocation or Utilization).
func printResourcePercentiles(results []utils.NodeResult, summaryContext string, resourceName string, out io.Writer) {
	// Create a copy to sort independently
	sortedResults := make([]utils.NodeResult, len(results))
	copy(sortedResults, results)

	var contextVerb string
	if summaryContext == "Allocation" {
		contextVerb = "requested"
	} else { // Utilization
		contextVerb = "used"
	}

	if resourceName == "CPU" {
		sort.Slice(sortedResults, func(i, j int) bool {
			return sortedResults[i].CPUPercent < sortedResults[j].CPUPercent
		})
		fmt.Fprintf(out, "\nCPU %s Percentiles (based on %% of allocatable CPU %s):\n", summaryContext, contextVerb)
	} else { // Memory
		sort.Slice(sortedResults, func(i, j int) bool {
			return sortedResults[i].MemPercent < sortedResults[j].MemPercent
		})
		fmt.Fprintf(out, "\nMemory %s Percentiles (based on %% of allocatable Memory %s):\n", summaryContext, contextVerb)
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
		fmt.Fprintln(out, "  No data available.")
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
			// ReqCPU stores actual usage in utilization context, and requested in allocation context
			valueString = utils.FormatCPU(nodeRes.ReqCPU)
			percentValue = nodeRes.CPUPercent
		} else { // Memory
			// ReqMem stores actual usage in utilization context, and requested in allocation context
			valueString = utils.FormatMemory(nodeRes.ReqMem.Value())
			percentValue = nodeRes.MemPercent
		}
		fmt.Fprintf(out, "  - %-12s: %s (%s: %s, %s%.1f%%%s)\n",
			p.name, nodeRes.Node.Name, strings.Title(contextVerb), valueString,
			ui.GetColorForPercentage(percentValue), percentValue, ui.ColorReset)
	}
}

// printTopHostPorts aggregates host port usage and prints the top 10.
// This is only relevant for the 'allocations' command.
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
