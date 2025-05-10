// Package cmd implements the subcommands for the kubectl-node-resources plugin.
package cmd

import (
	"context"
	"fmt"
	"text/tabwriter"

	// "os" // Not needed here based on allocations.go refactor

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	metricsv "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	// Use the correct module path
	"kubectl-node_resources/pkg/summary"
	"kubectl-node_resources/pkg/ui"
	"kubectl-node_resources/pkg/utils"
)

// newUtilizationCmd returns the utilization subcommand.
// This command displays actual node resource utilization (similar to 'kubectl top node').
func newUtilizationCmd(streams genericclioptions.IOStreams) *cobra.Command {
	opts := genericclioptions.NewConfigFlags(true)
	var sortBy string

	cmd := &cobra.Command{
		Use:   "utilization [node-selector]",
		Short: "Show actual node utilization (similar to kubectl top node)",
		Long: `Displays a table of nodes with their allocatable CPU and memory,
the actual CPU and memory currently used by them, and the percentage
of allocatable resources utilized.

Nodes can be filtered by a label selector. This command requires the
Kubernetes metrics-server to be installed and running in the cluster.`,
		Example: `  # Show utilization for all nodes, sorted by CPU percentage
  kubectl node-resources utilization "" --sort-by=cpu-percent

  # Show utilization for nodes with label 'role=worker'
  kubectl node-resources utilization "role=worker"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sortBy != utils.SortByCPUPercent && sortBy != utils.SortByMemoryPercent && sortBy != utils.SortByNodeName {
				return fmt.Errorf("invalid --sort-by value. Must be one of: %s, %s, %s", utils.SortByCPUPercent, utils.SortByMemoryPercent, utils.SortByNodeName)
			}
			klog.V(4).InfoS("Starting utilization command", "selector", args[0], "sortBy", sortBy)
			return runUtilization(cmd.Context(), opts, args[0], sortBy, streams)
		},
	}

	cmd.Flags().StringVar(&sortBy, "sort-by", utils.SortByCPUPercent, fmt.Sprintf("Sort nodes by: %s, %s, or %s", utils.SortByCPUPercent, utils.SortByMemoryPercent, utils.SortByNodeName))
	opts.AddFlags(cmd.Flags())
	return cmd
}

// runUtilization executes the core logic for the utilization command.
// It fetches node data and metrics, calculates resource utilization, and prints the results.
func runUtilization(ctx context.Context, configFlags *genericclioptions.ConfigFlags, nodeSelector string, sortBy string, streams genericclioptions.IOStreams) error {
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

		results[i] = utils.NodeResult{
			Node:       node,
			ReqCPU:     actCPU, // For utilization, ReqCPU/Mem store actual used resources
			ReqMem:     actMem,
			CPUPercent: utils.CalculatePercent(actCPU.AsApproximateFloat64(), allocCPU.AsApproximateFloat64()),
			MemPercent: utils.CalculatePercent(float64(actMem.Value()), float64(allocMem.Value())),
			// HostPorts are not relevant for utilization command
		}
	}

	utils.SortResults(results, sortBy)
	klog.V(4).InfoS("Utilization results sorted", "sortBy", sortBy)

	tw := tabwriter.NewWriter(streams.Out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NODE\tCPU\tMEM\tCPU USED\tMEM USED\tCPU USE%\tMEM USE%")

	for _, res := range results {
		allocCPU := res.Node.Status.Allocatable.Cpu()
		allocMem := res.Node.Status.Allocatable.Memory()

		cpuColor := ui.GetColorForPercentage(res.CPUPercent)
		memColor := ui.GetColorForPercentage(res.MemPercent)

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s%.1f%%%s\t%s%.1f%%%s\n",
			res.Node.Name, utils.FormatCPU(*allocCPU), utils.FormatMemory(allocMem.Value()),
			utils.FormatCPU(res.ReqCPU), utils.FormatMemory(res.ReqMem.Value()), // ReqCPU/Mem store actual usage here
			cpuColor, res.CPUPercent, ui.ColorReset,
			memColor, res.MemPercent, ui.ColorReset)
	}
	tw.Flush()

	// Call PrintUtilizationSummary from pkg/summary
	summary.PrintUtilizationSummary(results, streams.Out)

	klog.V(4).InfoS("Utilization command finished successfully")
	return nil
}
