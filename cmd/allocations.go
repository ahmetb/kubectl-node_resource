// Package cmd implements the subcommands for the kubectl-node-resources plugin.
package cmd

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	// TODO: Update this import path once progressbar_utils.go is moved
	// For now, assuming it's in the main package or a directly accessible path.
	// If progressbarHelper is in main, we might need to pass it or rethink its direct usage here.
	// For now, let's assume newProgressBarHelper is accessible.
	// Similar consideration for GetColorForPercentage and colorReset from color_utils.go

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
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

// newAllocationsCmd returns the allocations subcommand.
// This command displays resource allocations (sum of pod resource requests) for nodes.
func newAllocationsCmd(streams genericclioptions.IOStreams) *cobra.Command {
	opts := genericclioptions.NewConfigFlags(true)
	var (
		sortBy        string
		showHostPorts bool
	)

	cmd := &cobra.Command{
		Use:   "allocations [node-selector]",
		Short: "Show resource allocations for nodes (sum of pod resource requests)",
		Long: `Displays a table of nodes with their allocatable CPU and memory,
the sum of CPU and memory requests from pods running on them,
and the percentage of allocatable resources requested.

Optionally, it can show host ports used by containers on each node.
Nodes can be filtered by a label selector.`,
		Example: `  # Show allocations for all nodes, sorted by CPU percentage
  kubectl node-resources allocations "" --sort-by=cpu-percent

  # Show allocations for nodes with label 'role=worker', showing host ports
  kubectl node-resources allocations "role=worker" --show-host-ports

  # Show allocations for a specific node
  kubectl node-resources allocations "kubernetes.io/hostname=node1"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sortBy != utils.SortByCPUPercent && sortBy != utils.SortByMemoryPercent && sortBy != utils.SortByNodeName {
				return fmt.Errorf("invalid --sort-by value. Must be one of: %s, %s, %s", utils.SortByCPUPercent, utils.SortByMemoryPercent, utils.SortByNodeName)
			}
			klog.V(4).InfoS("Starting allocations command", "selector", args[0], "sortBy", sortBy, "showHostPorts", showHostPorts)

			return runAllocations(cmd.Context(), opts, args[0], sortBy, showHostPorts, streams)
		},
	}

	cmd.Flags().StringVar(&sortBy, "sort-by", utils.SortByCPUPercent, fmt.Sprintf("Sort nodes by: %s, %s, or %s", utils.SortByCPUPercent, utils.SortByMemoryPercent, utils.SortByNodeName))
	cmd.Flags().BoolVar(&showHostPorts, "show-host-ports", false, "Show host ports used by containers on each node")
	opts.AddFlags(cmd.Flags())
	return cmd
}

// runAllocations executes the core logic for the allocations command.
// It fetches node and pod data, calculates resource allocations, and prints the results.
func runAllocations(ctx context.Context, configFlags *genericclioptions.ConfigFlags, nodeSelector string, sortBy string, showHostPorts bool, streams genericclioptions.IOStreams) error {
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

	tw := tabwriter.NewWriter(streams.Out, 0, 8, 2, ' ', 0)
	header := "NODE\tCPU\tMEM\tCPU REQ\tMEM REQ\tCPU%\tMEM%"
	if showHostPorts {
		header += "\tHOST PORTS"
	}
	fmt.Fprintln(tw, header)

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
			}
			klog.V(5).InfoS("Finished processing node", "nodeName", node.Name, "reqCPU", totalCPU.String(), "reqMem", totalMem.String())

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

	for _, res := range results {
		allocCPU := res.Node.Status.Allocatable.Cpu()
		allocMem := res.Node.Status.Allocatable.Memory()

		cpuColor := ui.GetColorForPercentage(res.CPUPercent)
		memColor := ui.GetColorForPercentage(res.MemPercent)

		row := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s%.1f%%%s\t%s%.1f%%%s",
			res.Node.Name, utils.FormatCPU(*allocCPU), utils.FormatMemory(allocMem.Value()),
			utils.FormatCPU(res.ReqCPU), utils.FormatMemory(res.ReqMem.Value()),
			cpuColor, res.CPUPercent, ui.ColorReset,
			memColor, res.MemPercent, ui.ColorReset)

		if showHostPorts {
			portStrings := make([]string, len(res.HostPorts))
			for i, port := range res.HostPorts {
				portStrings[i] = strconv.Itoa(int(port))
			}
			if len(portStrings) == 0 {
				row += "\t-"
			} else {
				row += "\t" + strings.Join(portStrings, ",")
			}
		}
		fmt.Fprintln(tw, row)
	}
	tw.Flush()

	// Call PrintAllocationSummary from pkg/summary
	// Ensure streams.Out is passed, which implements io.Writer
	summary.PrintAllocationSummary(results, showHostPorts, streams.Out)

	klog.V(4).InfoS("Allocations command finished successfully")
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
