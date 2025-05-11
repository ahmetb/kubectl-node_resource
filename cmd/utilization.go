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

// Package cmd implements the subcommands for the kubectl node-resource plugin.
package cmd

import (
	"context"
	"fmt"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	metricsv "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"kubectl-node_resources/pkg/options" // Changed
	"kubectl-node_resources/pkg/output"
	"kubectl-node_resources/pkg/summary"
	"kubectl-node_resources/pkg/ui"
	"kubectl-node_resources/pkg/utils"
)

// newUtilizationCmd returns a command displays actual node resource utilization (similar to 'kubectl top node').
func newUtilizationCmd(streams genericclioptions.IOStreams) *cobra.Command {
	opts := genericclioptions.NewConfigFlags(true)
	var (
		sortBy     string
		summaryOpt string
		// display options are now in a struct
	)
	displayOpts := options.DisplayOptions{} // Initialize the struct from pkg/options

	cmd := &cobra.Command{
		Use:   "utilization [node-selector]",
		Short: "Show actual node utilization (similar to kubectl top node)",
		Long: `Displays a table of nodes with their allocatable CPU and memory,
the actual CPU and memory currently used by them, and the percentage
of allocatable resources utilized.

Nodes can be filtered by a label selector. This command requires the
Kubernetes metrics-server to be installed and running in the cluster.`,
		Example: `  # Show utilization for all nodes, sorted by CPU percentage
  kubectl node-resource utilization --sort-by=cpu-percent

  # Show utilization for nodes with label 'role=worker'
  kubectl node-resource utilization "role=worker"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sortBy != utils.SortByCPUPercent && sortBy != utils.SortByMemoryPercent && sortBy != utils.SortByNodeName {
				return fmt.Errorf("invalid --sort-by value. Must be one of: %s, %s, or %s", utils.SortByCPUPercent, utils.SortByMemoryPercent, utils.SortByNodeName)
			}
			if summaryOpt != utils.SummaryShow && summaryOpt != utils.SummaryOnly && summaryOpt != utils.SummaryHide {
				return fmt.Errorf("invalid --summary value. Must be one of: %s, %s, %s", utils.SummaryShow, utils.SummaryOnly, utils.SummaryHide)
			}
			var selector string
			if len(args) > 0 {
				selector = args[0]
			}
			klog.V(4).InfoS("Starting utilization command", "selector", selector, "sortBy", sortBy,
				"showCPU", displayOpts.ShowCPU, "showMemory", displayOpts.ShowMemory,
				"showFree", displayOpts.ShowFree, "summary", summaryOpt, "json", displayOpts.JSONOutput)

			runOpts := utilizationRunOptions{
				configFlags:  opts,
				streams:      streams,
				nodeSelector: selector,
				sortBy:       sortBy,
				summaryOpt:   summaryOpt,
				displayOpts:  displayOpts,
			}
			return runUtilization(cmd.Context(), runOpts)
		},
	}

	cmd.Flags().StringVar(&sortBy, "sort-by", utils.SortByCPUPercent, fmt.Sprintf("Sort nodes by: %s, %s, or %s", utils.SortByCPUPercent, utils.SortByMemoryPercent, utils.SortByNodeName))
	cmd.Flags().BoolVar(&displayOpts.ShowCPU, "show-cpu", true, "Show CPU utilization")
	cmd.Flags().BoolVar(&displayOpts.ShowMemory, "show-memory", true, "Show memory utilization")
	cmd.Flags().BoolVar(&displayOpts.ShowFree, "show-free", false, "Show free CPU and Memory on each node")
	cmd.Flags().StringVar(&summaryOpt, "summary", utils.SummaryShow, fmt.Sprintf("Summary display option: %s, %s, or %s", utils.SummaryShow, utils.SummaryOnly, utils.SummaryHide))
	cmd.Flags().BoolVar(&displayOpts.JSONOutput, "json", false, "Output in JSON format")
	opts.AddFlags(cmd.Flags())
	return cmd
}

type utilizationRunOptions struct {
	configFlags  *genericclioptions.ConfigFlags
	streams      genericclioptions.IOStreams
	nodeSelector string
	sortBy       string
	summaryOpt   string
	displayOpts  options.DisplayOptions // Use options.DisplayOptions
}

// runUtilization executes the core logic for the utilization command.
// It fetches node data and metrics, calculates resource utilization, and prints the results.
func runUtilization(ctx context.Context, opts utilizationRunOptions) error {
	config, err := opts.configFlags.ToRESTConfig()
	if err != nil {
		return fmt.Errorf("failed to build Kubernetes client config: %w", err)
	}
	klog.V(5).InfoS("REST config created for utilization")

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}
	metricsClient, err := metricsclient.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create metrics client: %w", err)
	}

	// Context for Kubernetes API calls is now passed in (cmd.Context())
	var allNodes []corev1.Node
	err = ui.RunWithSpinner("Querying nodes from the API server...", func() error {
		var fetchErr error
		allNodes, fetchErr = utils.GetAllNodesWithPagination(ctx, clientset, opts.nodeSelector)
		return fetchErr
	}, opts.streams.ErrOut)

	if err != nil {
		return fmt.Errorf("failed to get all nodes with pagination for utilization (selector: %s): %w", opts.nodeSelector, err)
	}
	if len(allNodes) == 0 {
		klog.InfoS("No nodes found with the given selector for utilization.", "selector", opts.nodeSelector)
		fmt.Fprintln(opts.streams.Out, "No nodes found with the given selector for utilization.")
		return nil
	}

	klog.V(4).InfoS("Fetching node metrics")
	// Note: NodeMetricses().List does not support pagination in the same way core resources do.
	// It typically returns all metrics in one go.
	var metricsList *metricsv.NodeMetricsList
	err = ui.RunWithSpinner("Querying metrics API for node stats...", func() error {
		var fetchErr error
		metricsList, fetchErr = metricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
		return fetchErr
	}, opts.streams.ErrOut)

	if err != nil {
		// klog.ErrorS is appropriate here as it's a significant failure point for this command.
		klog.ErrorS(err, "Failed to list node metrics. Ensure metrics-server is installed and running.")
		return fmt.Errorf("failed to list node metrics: %w. Ensure metrics-server is installed and running", err)
	}
	metricsMap := make(map[string]metricsv.NodeMetrics)
	for _, nm := range metricsList.Items {
		metricsMap[nm.Name] = nm
	}
	klog.V(4).InfoS("Node metrics fetched", "count", len(metricsList.Items))

	results := make([]utils.NodeResult, len(allNodes))
	for i, node := range allNodes {
		allocCPU := node.Status.Allocatable.Cpu()
		allocMem := node.Status.Allocatable.Memory()
		var actCPU, actMem resource.Quantity

		if nm, ok := metricsMap[node.Name]; ok {
			actCPU = nm.Usage[corev1.ResourceCPU]
			actMem = nm.Usage[corev1.ResourceMemory]
		} else {
			klog.V(2).InfoS("Metrics not found for node, assuming zero usage for all resources", "nodeName", node.Name)
			actCPU = *resource.NewQuantity(0, resource.DecimalSI)
			actMem = *resource.NewQuantity(0, resource.BinarySI)
		}

		cpuPercent := utils.CalculatePercent(actCPU.AsApproximateFloat64(), allocCPU.AsApproximateFloat64())
		memPercent := utils.CalculatePercent(float64(actMem.Value()), float64(allocMem.Value()))

		freeCPU := allocCPU.DeepCopy()
		freeCPU.Sub(actCPU)
		if freeCPU.Sign() < 0 {
			freeCPU = *resource.NewQuantity(0, resource.DecimalSI)
		}

		freeMem := allocMem.DeepCopy()
		freeMem.Sub(actMem)
		if freeMem.Sign() < 0 {
			freeMem = *resource.NewQuantity(0, resource.BinarySI)
		}

		results[i] = utils.NodeResult{
			Node:       node,
			ReqCPU:     actCPU,
			ReqMem:     actMem,
			CPUPercent: cpuPercent,
			MemPercent: memPercent,
			FreeCPU:    freeCPU,
			FreeMem:    freeMem,
		}
	}

	utils.SortResults(results, opts.sortBy)
	klog.V(4).InfoS("Utilization results sorted", "sortBy", opts.sortBy)

	// Check if there's anything to display before proceeding
	// For utilization, ShowHostPorts and ShowEphemeralStorage are implicitly false in DisplayOpts.
	if !opts.displayOpts.JSONOutput && opts.summaryOpt == utils.SummaryHide && !opts.displayOpts.HasPrimaryDataColumns() {
		fmt.Fprintln(opts.streams.ErrOut, "Error: No data selected for display. Please enable at least one of --show-cpu, --show-memory, or ensure summary is not hidden, or use --json.")
		return fmt.Errorf("no data selected for display")
	}

	if opts.displayOpts.JSONOutput {
		// JSON Output Path
		jsonData, err := output.GetJSONOutput(results, utils.CmdTypeUtilization, opts.displayOpts, opts.summaryOpt,
			func(r []utils.NodeResult, currentDisplayOpts options.DisplayOptions, cType utils.CmdType) (*output.JSONSummary, error) {
				// For utilization, ShowHostPorts and ShowEphemeralStorage will be false in currentDisplayOpts if not set by flags (which they aren't for this cmd).
				return summary.GetNodeResourceSummaryData(r, currentDisplayOpts, cType)
			})
		if err != nil {
			return fmt.Errorf("failed to prepare JSON data for utilization: %w", err)
		}
		if err := output.PrintJSON(jsonData, opts.streams); err != nil {
			return fmt.Errorf("failed to print JSON output for utilization: %w", err)
		}
		klog.V(4).InfoS("JSON output printed successfully")
		return nil
	}

	// Table Output Path
	table := tablewriter.NewWriter(opts.streams.Out)
	var headerVals []string
	headerVals = append(headerVals, "NODE")

	if opts.displayOpts.ShowCPU {
		headerVals = append(headerVals, "CPU", "CPU USED", "CPU%")
	}
	if opts.displayOpts.ShowMemory {
		headerVals = append(headerVals, "MEMORY", "MEM USED", "MEM%")
	}
	// Ephemeral storage and host ports are not shown for utilization

	if opts.displayOpts.ShowFree {
		if opts.displayOpts.ShowCPU {
			headerVals = append(headerVals, "FREE CPU")
		}
		if opts.displayOpts.ShowMemory {
			headerVals = append(headerVals, "FREE MEMORY")
		}
	}
	table.SetHeader(headerVals)
	setKubectlTableStyle(table)

	for _, res := range results {
		allocCPU := res.Node.Status.Allocatable.Cpu()
		allocMem := res.Node.Status.Allocatable.Memory()

		cpuColor := ui.PercentFontColor(res.CPUPercent)
		memColor := ui.PercentFontColor(res.MemPercent)

		var rowValues []string
		rowValues = append(rowValues, res.Node.Name)

		if opts.displayOpts.ShowCPU {
			rowValues = append(rowValues,
				utils.FormatCPU(*allocCPU),
				utils.FormatCPU(res.ReqCPU), // Actual used CPU
				fmt.Sprintf("%s%.1f%%%s", cpuColor, res.CPUPercent, ui.ColorReset),
			)
		}
		if opts.displayOpts.ShowMemory {
			rowValues = append(rowValues,
				utils.FormatMemory(allocMem.Value()),
				utils.FormatMemory(res.ReqMem.Value()), // Actual used Memory
				fmt.Sprintf("%s%.1f%%%s", memColor, res.MemPercent, ui.ColorReset),
			)
		}

		if opts.displayOpts.ShowFree {
			if opts.displayOpts.ShowCPU {
				freeCPUColor := ui.PercentBackgroundColor(res.CPUPercent)
				rowValues = append(rowValues,
					fmt.Sprintf("%s%s%s", freeCPUColor, utils.FormatCPU(res.FreeCPU), ui.ColorReset),
				)
			}
			if opts.displayOpts.ShowMemory {
				freeMemColor := ui.PercentBackgroundColor(res.MemPercent)
				rowValues = append(rowValues,
					fmt.Sprintf("%s%s%s", freeMemColor, utils.FormatMemory(res.FreeMem.Value()), ui.ColorReset),
				)
			}
		}
		table.Append(rowValues)
	}

	if opts.summaryOpt != utils.SummaryOnly {
		if len(headerVals) > 1 { // Only render if there are columns to show (besides NODE)
			table.Render()
		} else if !opts.displayOpts.JSONOutput && opts.summaryOpt == utils.SummaryHide {
			fmt.Fprintln(opts.streams.Out, "No data to display with current flags.")
		}
	}

	if opts.summaryOpt == utils.SummaryShow || opts.summaryOpt == utils.SummaryOnly {
		summary.PrintNodeResourceSummary(results, opts.displayOpts, opts.streams.Out, utils.CmdTypeUtilization)
	}

	klog.V(4).InfoS("Utilization command finished successfully")
	return nil
}
