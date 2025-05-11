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

	"kubectl-node_resources/pkg/output"
	"kubectl-node_resources/pkg/summary"
	"kubectl-node_resources/pkg/ui"
	"kubectl-node_resources/pkg/utils"
)

// newUtilizationCmd returns the utilization subcommand.
// This command displays actual node resource utilization (similar to 'kubectl top node').
func newUtilizationCmd(streams genericclioptions.IOStreams) *cobra.Command {
	opts := genericclioptions.NewConfigFlags(true)
	var (
		sortBy     string
		showFree   bool
		summaryOpt string
		jsonOutput bool
	)

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
				return fmt.Errorf("invalid --sort-by value. Must be one of: %s, %s, %s", utils.SortByCPUPercent, utils.SortByMemoryPercent, utils.SortByNodeName)
			}
			if summaryOpt != utils.SummaryShow && summaryOpt != utils.SummaryOnly && summaryOpt != utils.SummaryHide {
				return fmt.Errorf("invalid --summary value. Must be one of: %s, %s, %s", utils.SummaryShow, utils.SummaryOnly, utils.SummaryHide)
			}
			var selector string
			if len(args) > 0 {
				selector = args[0]
			}
			klog.V(4).InfoS("Starting utilization command", "selector", selector, "sortBy", sortBy, "showFree", showFree, "summary", summaryOpt, "json", jsonOutput)
			return runUtilization(cmd.Context(), opts, selector, sortBy, showFree, summaryOpt, jsonOutput, streams)
		},
	}

	cmd.Flags().StringVar(&sortBy, "sort-by", utils.SortByCPUPercent, fmt.Sprintf("Sort nodes by: %s, %s, or %s", utils.SortByCPUPercent, utils.SortByMemoryPercent, utils.SortByNodeName))
	cmd.Flags().BoolVar(&showFree, "show-free", false, "Show free CPU and Memory on each node") // Added flag
	cmd.Flags().StringVar(&summaryOpt, "summary", utils.SummaryShow, fmt.Sprintf("Summary display option: %s, %s, or %s", utils.SummaryShow, utils.SummaryOnly, utils.SummaryHide))
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format") // Added --json flag
	opts.AddFlags(cmd.Flags())
	return cmd
}

// runUtilization executes the core logic for the utilization command.
// It fetches node data and metrics, calculates resource utilization, and prints the results.
func runUtilization(ctx context.Context, configFlags *genericclioptions.ConfigFlags, nodeSelector string, sortBy string, showFree bool, summaryOpt string, jsonOutputFlag bool, streams genericclioptions.IOStreams) error {
	config, err := configFlags.ToRESTConfig()
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
	allNodes, err := utils.GetAllNodesWithPagination(ctx, clientset, nodeSelector)
	if err != nil {
		return fmt.Errorf("failed to get all nodes with pagination for utilization (selector: %s): %w", nodeSelector, err)
	}
	if len(allNodes) == 0 {
		klog.InfoS("No nodes found with the given selector for utilization.", "selector", nodeSelector)
		fmt.Fprintln(streams.Out, "No nodes found with the given selector for utilization.")
		return nil
	}

	klog.V(4).InfoS("Fetching node metrics")
	// Note: NodeMetricses().List does not support pagination in the same way core resources do.
	// It typically returns all metrics in one go.
	metricsList, err := metricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
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
			klog.V(2).InfoS("Metrics not found for node, assuming zero usage", "nodeName", node.Name)
			// Ensure these are non-nil quantities
			actCPU = *resource.NewQuantity(0, resource.DecimalSI)
			actMem = *resource.NewQuantity(0, resource.BinarySI)
		}

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
			ReqCPU:     actCPU, // For utilization, ReqCPU/Mem store actual used resources
			ReqMem:     actMem,
			CPUPercent: utils.CalculatePercent(actCPU.AsApproximateFloat64(), allocCPU.AsApproximateFloat64()),
			MemPercent: utils.CalculatePercent(float64(actMem.Value()), float64(allocMem.Value())),
			FreeCPU:    freeCPU, // Store calculated free CPU
			FreeMem:    freeMem, // Store calculated free Memory
			// HostPorts are not relevant for utilization command
		}
	}

	utils.SortResults(results, sortBy)
	klog.V(4).InfoS("Utilization results sorted", "sortBy", sortBy)

	if jsonOutputFlag {
		// JSON Output Path
		jsonData, err := output.GetJSONOutput(results, output.CmdTypeUtilization, showFree, false /*showHostPorts*/, summaryOpt,
			func(r []utils.NodeResult, shp bool, cType string) (*output.JSONSummary, error) {
				// cType here will be output.CmdTypeUtilization, passed by GetJSONOutput
				return summary.GetNodeResourceSummaryData(r, shp, cType)
			})
		if err != nil {
			return fmt.Errorf("failed to prepare JSON data for utilization: %w", err)
		}
		if err := output.PrintJSON(jsonData, streams); err != nil {
			return fmt.Errorf("failed to print JSON output for utilization: %w", err)
		}
		klog.V(4).InfoS("JSON output printed successfully")
		return nil
	}
	// Table Output Path
	table := tablewriter.NewWriter(streams.Out)
	headerVals := []string{"NODE", "CPU", "CPU USED", "CPU%", "MEMORY", "MEM USED", "MEM%"}
	if showFree {
		headerVals = append(headerVals, "FREE CPU", "FREE MEMORY")
	}
	table.SetHeader(headerVals)
	setKubectlTableStyle(table)

	for _, res := range results {
		allocCPU := res.Node.Status.Allocatable.Cpu()
		allocMem := res.Node.Status.Allocatable.Memory()

		cpuColor := ui.PercentFontColor(res.CPUPercent)
		memColor := ui.PercentFontColor(res.MemPercent)

		rowValues := []string{
			res.Node.Name,
			utils.FormatCPU(*allocCPU),
			utils.FormatCPU(res.ReqCPU), // Actual used CPU
			fmt.Sprintf("%s%.1f%%%s", cpuColor, res.CPUPercent, ui.ColorReset),
			utils.FormatMemory(allocMem.Value()),
			utils.FormatMemory(res.ReqMem.Value()), // Actual used Memory
			fmt.Sprintf("%s%.1f%%%s", memColor, res.MemPercent, ui.ColorReset),
		}

		if showFree {
			freeCPUColor := ui.PercentBackgroundColor(res.CPUPercent) // Color based on utilization %
			freeMemColor := ui.PercentBackgroundColor(res.MemPercent) // Color based on utilization %
			rowValues = append(rowValues,
				fmt.Sprintf("%s%s%s", freeCPUColor, utils.FormatCPU(res.FreeCPU), ui.ColorReset),
				fmt.Sprintf("%s%s%s", freeMemColor, utils.FormatMemory(res.FreeMem.Value()), ui.ColorReset),
			)
		}
		table.Append(rowValues)
	}

	if summaryOpt != utils.SummaryOnly {
		table.Render()
	}

	if summaryOpt == utils.SummaryShow || summaryOpt == utils.SummaryOnly {
		summary.PrintNodeResourceSummary(results, false /*showHostPorts*/, streams.Out, output.CmdTypeUtilization)
	}

	klog.V(4).InfoS("Utilization command finished successfully")
	return nil
}
