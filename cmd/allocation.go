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
	"sort"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"kubectl-node_resources/pkg/options" // Changed
	"kubectl-node_resources/pkg/output"
	"kubectl-node_resources/pkg/summary"
	"kubectl-node_resources/pkg/ui"
	"kubectl-node_resources/pkg/utils"
)

// newAllocationCmd returns a command displays resource allocation (sum of pod resource requests) for nodes.
func newAllocationCmd(streams genericclioptions.IOStreams) *cobra.Command {
	opts := genericclioptions.NewConfigFlags(true)
	var (
		sortBy     string
		summaryOpt string
		// display options are now in a struct
	)
	displayOpts := options.DisplayOptions{} // Initialize the struct from pkg/options

	cmd := &cobra.Command{
		Use:   "allocation [node-selector]",
		Short: "Show resource allocation for nodes (sum of pod resource requests)",
		Long: `Displays a table of nodes with their allocatable CPU and memory,
the sum of CPU and memory requests from pods running on them,
and the percentage of allocatable resources requested.

Optionally, it can show host ports used by containers on each node.
Nodes can be filtered by a label selector.`,
		Example: `  # Show allocation for, sorted by CPU percentage
  kubectl node-resource allocation --sort-by=cpu-percent

  # Show allocation for nodes with label 'role=worker', showing host ports
  kubectl node-resource allocation "role=worker" --show-host-ports

  # Show only the allocation summary for nodes
  kubectl node-resource allocation "role=worker" --summary=only

  # Show allocation for a specific node
  kubectl node-resource allocation "kubernetes.io/hostname=node1"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sortBy != utils.SortByCPUPercent && sortBy != utils.SortByMemoryPercent && sortBy != utils.SortByNodeName && sortBy != utils.SortByEphemeralStoragePercent {
				return fmt.Errorf("invalid --sort-by value. Must be one of: %s, %s, %s, or %s", utils.SortByCPUPercent, utils.SortByMemoryPercent, utils.SortByNodeName, utils.SortByEphemeralStoragePercent)
			}
			if summaryOpt != utils.SummaryShow && summaryOpt != utils.SummaryOnly && summaryOpt != utils.SummaryHide {
				return fmt.Errorf("invalid --summary value. Must be one of: %s, %s, %s", utils.SummaryShow, utils.SummaryOnly, utils.SummaryHide)
			}
			var selector string
			if len(args) > 0 {
				selector = args[0]
			}
			klog.V(4).InfoS("Starting allocation command", "selector", selector, "sortBy", sortBy,
				"showCPU", displayOpts.ShowCPU, "showMemory", displayOpts.ShowMemory,
				"showHostPorts", displayOpts.ShowHostPorts, "showFree", displayOpts.ShowFree,
				"showEphemeralStorage", displayOpts.ShowEphemeralStorage,
				"summary", summaryOpt, "json", displayOpts.JSONOutput)

			runOpts := allocationRunOptions{
				configFlags:  opts,
				streams:      streams,
				nodeSelector: selector,
				sortBy:       sortBy,
				summaryOpt:   summaryOpt,
				DisplayOpts:  displayOpts,
			}
			return runAllocation(cmd.Context(), runOpts)
		},
	}

	cmd.Flags().StringVar(&sortBy, "sort-by", utils.SortByCPUPercent, fmt.Sprintf("Sort nodes by: %s, %s, %s, or %s", utils.SortByCPUPercent, utils.SortByMemoryPercent, utils.SortByNodeName, utils.SortByEphemeralStoragePercent))
	cmd.Flags().BoolVar(&displayOpts.ShowCPU, "show-cpu", true, "Show CPU allocation/utilization")
	cmd.Flags().BoolVar(&displayOpts.ShowMemory, "show-memory", true, "Show memory allocation/utilization")
	cmd.Flags().BoolVar(&displayOpts.ShowHostPorts, "show-host-ports", false, "Show host ports used by containers on each node")
	cmd.Flags().BoolVar(&displayOpts.ShowEphemeralStorage, "show-ephemeral-storage", false, "Show ephemeral storage allocation/utilization")
	cmd.Flags().BoolVar(&displayOpts.ShowFree, "show-free", false, "Show free CPU and Memory on each node")
	cmd.Flags().StringVar(&summaryOpt, "summary", utils.SummaryShow, fmt.Sprintf("Summary display option: %s, %s, or %s", utils.SummaryShow, utils.SummaryOnly, utils.SummaryHide))
	cmd.Flags().BoolVar(&displayOpts.JSONOutput, "json", false, "Output in JSON format")
	opts.AddFlags(cmd.Flags())
	return cmd
}

type allocationRunOptions struct {
	configFlags  *genericclioptions.ConfigFlags
	streams      genericclioptions.IOStreams
	nodeSelector string
	sortBy       string
	summaryOpt   string
	DisplayOpts  options.DisplayOptions // Use options.DisplayOptions
}

// runAllocation executes the core logic for the allocation command.
// It fetches node and pod data, calculates resource allocations, and prints the results.
func runAllocation(ctx context.Context, opts allocationRunOptions) error {
	config, err := opts.configFlags.ToRESTConfig()
	if err != nil {
		return fmt.Errorf("failed to build Kubernetes client config: %w", err)
	}
	config.QPS = -1   // No client-side QPS limit
	config.Burst = -1 // No client-side burst limit
	klog.V(5).InfoS("REST config created", "qps", config.QPS, "burst", config.Burst)

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	// Context for Kubernetes API calls is now passed in (cmd.Context())
	allNodes, err := utils.GetAllNodesWithPagination(ctx, clientset, opts.nodeSelector)
	if err != nil {
		return fmt.Errorf("failed to get all nodes with pagination (selector: %s): %w", opts.nodeSelector, err)
	}
	if len(allNodes) == 0 {
		klog.InfoS("No nodes found with the given selector.", "selector", opts.nodeSelector)
		fmt.Fprintln(opts.streams.Out, "No nodes found with the given selector.")
		return nil
	}

	results := make([]utils.NodeResult, len(allNodes))

	progressHelper := ui.NewProgressBarHelper(len(allNodes), "listing pods on")

	g, gCtx := errgroup.WithContext(ctx) // Use the passed-in context for the errgroup
	g.SetLimit(20)                       // Concurrency limiter for k8s API calls

	klog.V(4).InfoS("Processing nodes in parallel with errgroup", "maxWorkers", 20, "nodeCount", len(allNodes))
	for i, node := range allNodes {
		i, node := i, node // Capture range variables
		g.Go(func() error {
			klog.V(5).InfoS("Processing node", "nodeName", node.Name)

			podList, err := clientset.CoreV1().Pods("").List(gCtx, metav1.ListOptions{
				FieldSelector:   "spec.nodeName=" + node.Name,
				ResourceVersion: "0",  // Get latest from cache if possible, otherwise direct API call
				Limit:           1000, // Limit pods per node call, adjust if necessary
			})
			if err != nil {
				return fmt.Errorf("failed to list pods for node %s: %w", node.Name, err)
			}
			klog.V(5).InfoS("Pods listed for node", "nodeName", node.Name, "podCount", len(podList.Items))

			var totalCPU, totalMem, totalEphemeralStorage resource.Quantity
			hostPortsMap := make(map[int32]struct{})

			for _, pod := range podList.Items {
				if gCtx.Err() != nil { // Check for context cancellation from errgroup
					return gCtx.Err()
				}
				podCPU, podMem, podEphemeralStorage := aggregatePodRequests(&pod)
				totalCPU.Add(podCPU)
				totalMem.Add(podMem)
				totalEphemeralStorage.Add(podEphemeralStorage)
				// Always calculate host ports
				for _, container := range pod.Spec.Containers {
					for _, port := range container.Ports {
						if port.HostPort > 0 {
							hostPortsMap[port.HostPort] = struct{}{}
						}
					}
				}
				for _, container := range pod.Spec.InitContainers { // Also check initContainers
					for _, port := range container.Ports {
						if port.HostPort > 0 {
							hostPortsMap[port.HostPort] = struct{}{}
						}
					}
				}
			}

			allocCPU := node.Status.Allocatable.Cpu()
			allocMem := node.Status.Allocatable.Memory()
			allocEphemeralStorage := node.Status.Allocatable[corev1.ResourceEphemeralStorage]
			cpuPercent := utils.CalculatePercent(totalCPU.AsApproximateFloat64(), allocCPU.AsApproximateFloat64())
			memPercent := utils.CalculatePercent(float64(totalMem.Value()), float64(allocMem.Value()))
			ephemeralStoragePercent := utils.CalculatePercent(float64(totalEphemeralStorage.Value()), float64(allocEphemeralStorage.Value()))

			freeCPU := allocCPU.DeepCopy()
			freeCPU.Sub(totalCPU)
			if freeCPU.Sign() < 0 { // Ensure free is not negative
				freeCPU = *resource.NewQuantity(0, resource.DecimalSI)
			}

			freeMem := allocMem.DeepCopy()
			freeMem.Sub(totalMem)
			if freeMem.Sign() < 0 { // Ensure free is not negative
				freeMem = *resource.NewQuantity(0, resource.BinarySI)
			}

			freeEphemeralStorage := allocEphemeralStorage.DeepCopy()
			freeEphemeralStorage.Sub(totalEphemeralStorage)
			if freeEphemeralStorage.Sign() < 0 { // Ensure free is not negative
				freeEphemeralStorage = *resource.NewQuantity(0, resource.BinarySI)
			}

			var currentHostPorts []int32
			if opts.DisplayOpts.ShowHostPorts {
				for port := range hostPortsMap {
					currentHostPorts = append(currentHostPorts, port)
				}
				sort.Slice(currentHostPorts, func(i, j int) bool { return currentHostPorts[i] < currentHostPorts[j] })
			}

			results[i] = utils.NodeResult{
				Node:       node,
				ReqCPU:     totalCPU,
				ReqMem:     totalMem,
				CPUPercent: cpuPercent,
				MemPercent: memPercent,
				HostPorts:  currentHostPorts,
				FreeCPU:    freeCPU,
				FreeMem:    freeMem,
				// Ephemeral Storage
				AllocEphemeralStorage:   allocEphemeralStorage,
				ReqEphemeralStorage:     totalEphemeralStorage,
				EphemeralStoragePercent: ephemeralStoragePercent,
				FreeEphemeralStorage:    freeEphemeralStorage,
			}
			klog.V(5).InfoS("Finished processing node", "nodeName", node.Name, "reqCPU", totalCPU.String(), "reqMem", totalMem.String(), "reqEphemeralStorage", totalEphemeralStorage.String(), "freeCPU", freeCPU.String(), "freeMem", freeMem.String(), "freeEphemeralStorage", freeEphemeralStorage.String())

			if progressHelper != nil {
				// Pass the same prefix, or an updated one if needed for this stage
				progressHelper.Increment("listing pods on")
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		if progressHelper != nil {
			progressHelper.Finish()
		}
		// klog.ErrorS is appropriate here as it's a top-level error for the command execution step.
		klog.ErrorS(err, "Error processing nodes")
		return fmt.Errorf("error processing nodes: %w", err)
	}

	if progressHelper != nil {
		progressHelper.Finish()
	}
	klog.V(4).InfoS("All nodes processed successfully")

	utils.SortResults(results, opts.sortBy)
	klog.V(4).InfoS("Results sorted", "sortBy", opts.sortBy)

	// Check if there's anything useful to display before proceeding
	if !opts.DisplayOpts.JSONOutput && !opts.DisplayOpts.HasPrimaryDataColumns() {
		fmt.Fprintln(opts.streams.ErrOut, "Error: No data columns selected for display. Please enable at least one of --show-cpu, --show-memory, --show-ephemeral-storage, --show-host-ports to display table data or generate a meaningful summary.")
		return fmt.Errorf("no data columns selected for display")
	}

	if opts.DisplayOpts.JSONOutput {
		// JSON Output Path
		jsonData, err := output.GetJSONOutput(results, utils.CmdTypeAllocation, opts.DisplayOpts, opts.summaryOpt,
			func(r []utils.NodeResult, currentDisplayOpts options.DisplayOptions, cType utils.CmdType) (*output.JSONSummary, error) {
				// cType here will be utils.CmdTypeAllocation, passed by GetJSONOutput
				return summary.GetNodeResourceSummaryData(r, currentDisplayOpts, cType)
			})
		if err != nil {
			return fmt.Errorf("failed to prepare JSON data for allocation: %w", err)
		}
		if err := output.PrintJSON(jsonData, opts.streams); err != nil {
			return fmt.Errorf("failed to print JSON output for allocation: %w", err)
		}
		klog.V(4).InfoS("JSON output printed successfully")
		return nil
	}

	// Table Output Path
	table := tablewriter.NewWriter(opts.streams.Out)
	var headerSlice []string
	headerSlice = append(headerSlice, "NODE")

	if opts.DisplayOpts.ShowCPU {
		headerSlice = append(headerSlice, "CPU", "CPU REQ", "CPU%")
	}
	if opts.DisplayOpts.ShowMemory {
		headerSlice = append(headerSlice, "MEMORY", "MEM REQ", "MEM%")
	}
	if opts.DisplayOpts.ShowEphemeralStorage {
		headerSlice = append(headerSlice, "EPHEMERAL", "EPH REQ", "EPH%")
	}
	if opts.DisplayOpts.ShowFree {
		if opts.DisplayOpts.ShowCPU {
			headerSlice = append(headerSlice, "FREE CPU")
		}
		if opts.DisplayOpts.ShowMemory {
			headerSlice = append(headerSlice, "FREE MEMORY")
		}
		if opts.DisplayOpts.ShowEphemeralStorage { // Assuming FREE EPH only makes sense if EPH is shown
			headerSlice = append(headerSlice, "FREE EPH")
		}
	}
	if opts.DisplayOpts.ShowHostPorts {
		headerSlice = append(headerSlice, "HOST PORTS")
	}
	table.SetHeader(headerSlice)
	setKubectlTableStyle(table)

	for _, res := range results {
		allocCPU := res.Node.Status.Allocatable.Cpu()
		allocMem := res.Node.Status.Allocatable.Memory()
		// allocEphemeralStorage is already in res.AllocEphemeralStorage

		cpuColor := ui.PercentFontColor(res.CPUPercent)
		memColor := ui.PercentFontColor(res.MemPercent)
		ephColor := ui.PercentFontColor(res.EphemeralStoragePercent)

		var rowValues []string
		rowValues = append(rowValues, res.Node.Name)

		if opts.DisplayOpts.ShowCPU {
			rowValues = append(rowValues,
				utils.FormatCPU(*allocCPU),
				utils.FormatCPU(res.ReqCPU),
				fmt.Sprintf("%s%.1f%%%s", cpuColor, res.CPUPercent, ui.ColorReset),
			)
		}
		if opts.DisplayOpts.ShowMemory {
			rowValues = append(rowValues,
				utils.FormatMemory(allocMem.Value()),
				utils.FormatMemory(res.ReqMem.Value()),
				fmt.Sprintf("%s%.1f%%%s", memColor, res.MemPercent, ui.ColorReset),
			)
		}

		if opts.DisplayOpts.ShowEphemeralStorage {
			rowValues = append(rowValues,
				utils.FormatMemory(res.AllocEphemeralStorage.Value()),
				utils.FormatMemory(res.ReqEphemeralStorage.Value()),
				fmt.Sprintf("%s%.1f%%%s", ephColor, res.EphemeralStoragePercent, ui.ColorReset),
			)
		}

		if opts.DisplayOpts.ShowFree {
			if opts.DisplayOpts.ShowCPU {
				freeCPUColor := ui.PercentBackgroundColor(res.CPUPercent) // Color based on usage
				rowValues = append(rowValues,
					fmt.Sprintf("%s%s%s", freeCPUColor, utils.FormatCPU(res.FreeCPU), ui.ColorReset),
				)
			}
			if opts.DisplayOpts.ShowMemory {
				freeMemColor := ui.PercentBackgroundColor(res.MemPercent) // Color based on usage
				rowValues = append(rowValues,
					fmt.Sprintf("%s%s%s", freeMemColor, utils.FormatMemory(res.FreeMem.Value()), ui.ColorReset),
				)
			}
			if opts.DisplayOpts.ShowEphemeralStorage { // Assuming FREE EPH only makes sense if EPH is shown
				freeEphColor := ui.PercentBackgroundColor(res.EphemeralStoragePercent) // Color based on usage
				rowValues = append(rowValues,
					fmt.Sprintf("%s%s%s", freeEphColor, utils.FormatMemory(res.FreeEphemeralStorage.Value()), ui.ColorReset),
				)
			}
		}

		if opts.DisplayOpts.ShowHostPorts {
			portStrings := make([]string, len(res.HostPorts))
			for i, port := range res.HostPorts {
				portStrings[i] = strconv.Itoa(int(port))
			}
			if len(portStrings) == 0 {
				rowValues = append(rowValues, "-")
			} else {
				rowValues = append(rowValues, strings.Join(portStrings, ","))
			}
		}
		table.Append(rowValues)
	}

	if opts.summaryOpt != utils.SummaryOnly {
		// Only render if there are columns to show (besides NODE)
		// This check is now complemented by the more comprehensive one at the beginning of output generation.
		if len(headerSlice) > 1 {
			table.Render()
		} else if !opts.DisplayOpts.JSONOutput && opts.summaryOpt == utils.SummaryHide {
			// This case should ideally be caught by the earlier check,
			// but as a fallback, print a message if table would be empty and no other output is planned.
			fmt.Fprintln(opts.streams.Out, "No data to display with current flags.")
		}
	}

	if opts.summaryOpt == utils.SummaryShow || opts.summaryOpt == utils.SummaryOnly {
		summary.PrintNodeResourceSummary(results, opts.DisplayOpts, opts.streams.Out, utils.CmdTypeAllocation)
	}

	klog.V(4).InfoS("Allocation command finished successfully")
	return nil
}

// aggregatePodRequests calculates the total CPU, memory, and ephemeral storage requests for a single pod,
// considering init containers as per Kubernetes resource accounting.
func aggregatePodRequests(pod *corev1.Pod) (resource.Quantity, resource.Quantity, resource.Quantity) {
	sumCPU := *resource.NewQuantity(0, resource.DecimalSI)
	sumMem := *resource.NewQuantity(0, resource.BinarySI)
	sumEphemeralStorage := *resource.NewQuantity(0, resource.BinarySI)

	// Regular containers
	for _, container := range pod.Spec.Containers {
		if req, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
			sumCPU.Add(req)
		}
		if req, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
			sumMem.Add(req)
		}
		if req, ok := container.Resources.Requests[corev1.ResourceEphemeralStorage]; ok {
			sumEphemeralStorage.Add(req)
		}
	}

	// Init containers: effective request is the max of (sum of app container requests, max init container request)
	// This logic needs to be applied carefully. The pod's effective request is the higher of:
	// 1. Sum of all app containers.
	// 2. Max of any init container.
	// So, we calculate sumCPU/sumMem for app containers first.
	// Then, we find the max request for init containers.
	// The final pod request for a resource is max(sum_app_container_resource, max_init_container_resource).

	maxInitCPU := *resource.NewQuantity(0, resource.DecimalSI)
	maxInitMem := *resource.NewQuantity(0, resource.BinarySI)
	maxInitEphemeralStorage := *resource.NewQuantity(0, resource.BinarySI)

	for _, container := range pod.Spec.InitContainers {
		if req, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
			if req.Cmp(maxInitCPU) > 0 {
				maxInitCPU = req.DeepCopy()
			}
		}
		if req, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
			if req.Cmp(maxInitMem) > 0 {
				maxInitMem = req.DeepCopy()
			}
		}
		if req, ok := container.Resources.Requests[corev1.ResourceEphemeralStorage]; ok {
			if req.Cmp(maxInitEphemeralStorage) > 0 {
				maxInitEphemeralStorage = req.DeepCopy()
			}
		}
	}

	// The pod's effective request for a resource is the maximum of
	// the sum of all app containers' requests for that resource and
	// the maximum of all init containers' requests for that resource.
	if maxInitCPU.Cmp(sumCPU) > 0 {
		sumCPU = maxInitCPU.DeepCopy()
	}
	if maxInitMem.Cmp(sumMem) > 0 {
		sumMem = maxInitMem.DeepCopy()
	}
	if maxInitEphemeralStorage.Cmp(sumEphemeralStorage) > 0 {
		sumEphemeralStorage = maxInitEphemeralStorage.DeepCopy()
	}

	return sumCPU, sumMem, sumEphemeralStorage
}

func setKubectlTableStyle(table *tablewriter.Table) {
	table.SetAutoWrapText(false)
	table.SetAutoFormatHeaders(true)
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetCenterSeparator("")
	table.SetColumnSeparator("")
	table.SetRowSeparator("")
	table.SetHeaderLine(false)
	table.SetBorder(false)
	table.SetTablePadding("  ") // two spaces for padding
	table.SetNoWhiteSpace(true)
}
