// Package cmd implements the subcommands for the kubectl node-resource plugin.
package cmd

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
	// TODO: Update this import path once progressbar_utils.go is moved
	// For now, assuming it's in the main package or a directly accessible path.
	// If progressbarHelper is in main, we might need to pass it or rethink its direct usage here.
	// For now, let's assume newProgressBarHelper is accessible.
	// Similar consideration for GetColorForPercentage and colorReset from color_utils.go

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"

	// Added for JSON output
	"kubectl-node_resources/pkg/output"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	// Corrected import path for utils package
	"kubectl-node_resources/pkg/summary"
	"kubectl-node_resources/pkg/utils"

	// TODO: Import progressbar utils from their new pkg location once moved
	"kubectl-node_resources/pkg/ui"
)

// newAllocationCmd returns the allocation subcommand.
// This command displays resource allocation (sum of pod resource requests) for nodes.
func newAllocationCmd(streams genericclioptions.IOStreams) *cobra.Command {
	opts := genericclioptions.NewConfigFlags(true)
	var (
		sortBy        string
		showHostPorts bool
		showFree      bool // Added for --show-free
		summaryOpt    string
		jsonOutput    bool // Added for --json
	)

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
			klog.V(4).InfoS("Starting allocation command", "selector", selector, "sortBy", sortBy, "showHostPorts", showHostPorts, "showFree", showFree, "summary", summaryOpt, "json", jsonOutput)
			// Pass jsonOutput to runAllocation
			return runAllocation(cmd.Context(), opts, selector, sortBy, showHostPorts, showFree, summaryOpt, jsonOutput, streams)
		},
	}

	cmd.Flags().StringVar(&sortBy, "sort-by", utils.SortByCPUPercent, fmt.Sprintf("Sort nodes by: %s, %s, or %s", utils.SortByCPUPercent, utils.SortByMemoryPercent, utils.SortByNodeName))
	cmd.Flags().BoolVar(&showHostPorts, "show-host-ports", false, "Show host ports used by containers on each node")
	cmd.Flags().BoolVar(&showFree, "show-free", false, "Show free CPU and Memory on each node") // Added flag
	cmd.Flags().StringVar(&summaryOpt, "summary", utils.SummaryShow, fmt.Sprintf("Summary display option: %s, %s, or %s", utils.SummaryShow, utils.SummaryOnly, utils.SummaryHide))
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format") // Added --json flag
	opts.AddFlags(cmd.Flags())
	return cmd
}

// runAllocation executes the core logic for the allocation command.
// It fetches node and pod data, calculates resource allocations, and prints the results.
func runAllocation(ctx context.Context, configFlags *genericclioptions.ConfigFlags, nodeSelector string, sortBy string, showHostPorts bool, showFree bool, summaryOpt string, jsonOutputFlag bool, streams genericclioptions.IOStreams) error {
	config, err := configFlags.ToRESTConfig()
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
	allNodes, err := utils.GetAllNodesWithPagination(ctx, clientset, nodeSelector)
	if err != nil {
		return fmt.Errorf("failed to get all nodes with pagination (selector: %s): %w", nodeSelector, err)
	}
	if len(allNodes) == 0 {
		klog.InfoS("No nodes found with the given selector.", "selector", nodeSelector)
		fmt.Fprintln(streams.Out, "No nodes found with the given selector.")
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

			var totalCPU, totalMem resource.Quantity
			hostPortsMap := make(map[int32]struct{})

			for _, pod := range podList.Items {
				if gCtx.Err() != nil { // Check for context cancellation from errgroup
					return gCtx.Err()
				}
				podCPU, podMem := aggregatePodRequests(&pod)
				totalCPU.Add(podCPU)
				totalMem.Add(podMem)
				if showHostPorts {
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
			}

			allocCPU := node.Status.Allocatable.Cpu()
			allocMem := node.Status.Allocatable.Memory()
			cpuPercent := utils.CalculatePercent(totalCPU.AsApproximateFloat64(), allocCPU.AsApproximateFloat64())
			memPercent := utils.CalculatePercent(float64(totalMem.Value()), float64(allocMem.Value()))

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

			var currentHostPorts []int32
			if showHostPorts {
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
				FreeCPU:    freeCPU, // Store calculated free CPU
				FreeMem:    freeMem, // Store calculated free Memory
			}
			klog.V(5).InfoS("Finished processing node", "nodeName", node.Name, "reqCPU", totalCPU.String(), "reqMem", totalMem.String(), "freeCPU", freeCPU.String(), "freeMem", freeMem.String())

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
		return fmt.Errorf("error processing nodes: %w", err) // Propagate the error
	}

	if progressHelper != nil {
		progressHelper.Finish()
	}
	klog.V(4).InfoS("All nodes processed successfully")

	utils.SortResults(results, sortBy)
	klog.V(4).InfoS("Results sorted", "sortBy", sortBy)

	if jsonOutputFlag {
		// JSON Output Path
		jsonData, err := output.GetJSONOutput(results, output.CmdTypeAllocation, showFree, showHostPorts, summaryOpt,
			func(r []utils.NodeResult, shp bool, cType string) (*output.JSONSummary, error) {
				// cType here will be output.CmdTypeAllocation, passed by GetJSONOutput
				return summary.GetNodeResourceSummaryData(r, shp, cType)
			})
		if err != nil {
			return fmt.Errorf("failed to prepare JSON data for allocation: %w", err)
		}
		if err := output.PrintJSON(jsonData, streams); err != nil {
			return fmt.Errorf("failed to print JSON output for allocation: %w", err)
		}
	} else {
		// Table Output Path
		table := tablewriter.NewWriter(streams.Out)
		headerSlice := []string{"NODE", "CPU", "CPU REQ", "CPU%", "MEMORY", "MEM REQ", "MEM%"}
		if showFree {
			headerSlice = append(headerSlice, "FREE CPU", "FREE MEMORY")
		}
		if showHostPorts {
			headerSlice = append(headerSlice, "HOST PORTS")
		}
		table.SetHeader(headerSlice)
		setKubectlTableStyle(table)

		for _, res := range results {
			allocCPU := res.Node.Status.Allocatable.Cpu()
			allocMem := res.Node.Status.Allocatable.Memory()

			cpuColor := ui.PercentFontColor(res.CPUPercent)
			memColor := ui.PercentFontColor(res.MemPercent)

			rowValues := []string{
				res.Node.Name,
				utils.FormatCPU(*allocCPU),
				utils.FormatCPU(res.ReqCPU),
				fmt.Sprintf("%s%.1f%%%s", cpuColor, res.CPUPercent, ui.ColorReset),
				utils.FormatMemory(allocMem.Value()),
				utils.FormatMemory(res.ReqMem.Value()),
				fmt.Sprintf("%s%.1f%%%s", memColor, res.MemPercent, ui.ColorReset),
			}

			if showFree {
				freeCPUColor := ui.PercentBackgroundColor(res.CPUPercent)
				freeMemColor := ui.PercentBackgroundColor(res.MemPercent)
				rowValues = append(rowValues,
					fmt.Sprintf("%s%s%s", freeCPUColor, utils.FormatCPU(res.FreeCPU), ui.ColorReset),
					fmt.Sprintf("%s%s%s", freeMemColor, utils.FormatMemory(res.FreeMem.Value()), ui.ColorReset),
				)
			}

			if showHostPorts {
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

		if summaryOpt != utils.SummaryOnly {
			table.Render()
		}

		if summaryOpt == utils.SummaryShow || summaryOpt == utils.SummaryOnly {
			summary.PrintNodeResourceSummary(results, showHostPorts, streams.Out, "allocation") // Added type
		}
	}

	klog.V(4).InfoS("Allocation command finished successfully")
	return nil
}

// aggregatePodRequests calculates the total CPU and memory requests for a single pod,
// considering init containers as per Kubernetes resource accounting.
func aggregatePodRequests(pod *corev1.Pod) (resource.Quantity, resource.Quantity) {
	sumCPU := *resource.NewQuantity(0, resource.DecimalSI)
	sumMem := *resource.NewQuantity(0, resource.BinarySI)

	// Regular containers
	for _, container := range pod.Spec.Containers {
		if req, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
			sumCPU.Add(req)
		}
		if req, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
			sumMem.Add(req)
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

	return sumCPU, sumMem
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
	table.SetTablePadding("  ") // pad with tabs
	table.SetNoWhiteSpace(true)
}
