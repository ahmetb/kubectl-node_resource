package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metricsv "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/pager"
	"k8s.io/klog/v2"
)

type nodeResult struct {
	node       corev1.Node
	reqCPU     resource.Quantity
	reqMem     resource.Quantity
	cpuPercent float64
	memPercent float64
	hostPorts  []int32
}

const (
	sortByCPUPercent    = "cpu-percent"
	sortByMemoryPercent = "memory-percent"
	sortByNodeName      = "name"
	nodeListLimit       = 500 // Page limit for node listing
)

func main() {
	// Initialize klog flags
	klog.InitFlags(nil)
	goflags := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(goflags) // klog.InitFlags needs a flag.FlagSet to register its flags.

	root := &cobra.Command{
		Use:   "kubectl-node-resources",
		Short: "A kubectl plugin to show node resource allocations and utilization",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Parse standard flags, then klog specific flags
			// This ensures klog flags are parsed from cobra's flags
			// and logtostderr is set if not provided.
			if err := flag.CommandLine.Parse([]string{}); err != nil {
				// This might happen if cobra flags are also present in flag.CommandLine
				// klog.ErrorS(err, "Failed to parse command line flags for klog")
				// We can often ignore this if klog flags are correctly passed via pflags
			}
			if f := flag.Lookup("logtostderr"); f != nil && f.Value.String() == "false" {
				if err := flag.Set("logtostderr", "true"); err != nil {
					klog.ErrorS(err, "Failed to set logtostderr")
				}
			}
			return nil
		},
	}

	root.PersistentFlags().AddGoFlagSet(goflags)
	root.AddCommand(newAllocationsCmd())
	root.AddCommand(newUtilizationCmd())

	if err := root.Execute(); err != nil {
		klog.ErrorS(err, "Error executing command")
		os.Exit(1)
	}
}

func getAllNodesWithPagination(ctx context.Context, clientset *kubernetes.Clientset, labelSelector string) ([]corev1.Node, error) {
	pg := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		opts.LabelSelector = labelSelector
		opts.Limit = nodeListLimit
		return clientset.CoreV1().Nodes().List(ctx, opts)
	})

	var allNodes []corev1.Node
	err := pg.EachListItem(ctx, metav1.ListOptions{}, func(obj runtime.Object) error {
		node, ok := obj.(*corev1.Node)
		if !ok {
			return fmt.Errorf("unexpected object type: %T", obj)
		}
		allNodes = append(allNodes, *node)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error paginating node list: %w", err)
	}
	return allNodes, nil
}

// newAllocationsCmd returns the allocations subcommand.
func newAllocationsCmd() *cobra.Command {
	opts := genericclioptions.NewConfigFlags(true)
	var (
		sortBy        string
		showHostPorts bool
	)

	cmd := &cobra.Command{
		Use:   "allocations [node-selector]",
		Short: "Show resource allocations for nodes (sum of pod resource requests)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sortBy != sortByCPUPercent && sortBy != sortByMemoryPercent && sortBy != sortByNodeName {
				return fmt.Errorf("invalid --sort-by value. Must be one of: cpu-percent, memory-percent, name")
			}
			klog.V(4).InfoS("Starting allocations command", "selector", args[0], "sortBy", sortBy, "showHostPorts", showHostPorts)

			nodeSelector := args[0]

			config, err := opts.ToRESTConfig()
			if err != nil {
				klog.ErrorS(err, "Failed to build Kubernetes client config")
				return err
			}
			config.QPS = -1
			config.Burst = -1
			klog.V(5).InfoS("REST config created", "qps", config.QPS, "burst", config.Burst)

			clientset, err := kubernetes.NewForConfig(config)
			if err != nil {
				klog.ErrorS(err, "Failed to create Kubernetes clientset")
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute) // Increased timeout for potentially many nodes
			defer cancel()

			allNodes, err := getAllNodesWithPagination(ctx, clientset, nodeSelector)
			if err != nil {
				klog.ErrorS(err, "Failed to get all nodes with pagination", "selector", nodeSelector)
				return err
			}
			if len(allNodes) == 0 {
				klog.InfoS("No nodes found with the given selector.", "selector", nodeSelector)
				return nil
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
			header := "NODE\tCPU\tMEM\tCPU REQ\tMEM REQ\tCPU%\tMEM%"
			if showHostPorts {
				header += "\tHOST PORTS"
			}
			fmt.Fprintln(tw, header)

			var wg sync.WaitGroup
			results := make([]nodeResult, len(allNodes))
			sem := make(chan struct{}, 20)
			klog.V(4).InfoS("Processing nodes in parallel", "maxWorkers", 20, "nodeCount", len(allNodes))
			for i, node := range allNodes {
				wg.Add(1)
				sem <- struct{}{}
				go func(i int, node corev1.Node) {
					defer wg.Done()
					defer func() { <-sem }()
					klog.V(5).InfoS("Processing node", "nodeName", node.Name)

					podList, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
						FieldSelector:   "spec.nodeName=" + node.Name,
						ResourceVersion: "0",
						Limit:           1000, // Limit pods per node call, though usually not an issue
					})
					if err != nil {
						klog.ErrorS(err, "Failed to list pods for node", "nodeName", node.Name)
						results[i] = nodeResult{node: node}
						return
					}
					klog.V(5).InfoS("Pods listed for node", "nodeName", node.Name, "podCount", len(podList.Items))

					var totalCPU, totalMem resource.Quantity
					hostPortsMap := make(map[int32]struct{})

					for _, pod := range podList.Items {
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
							for _, container := range pod.Spec.InitContainers {
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
					cpuPercent := calculatePercent(totalCPU.AsApproximateFloat64(), allocCPU.AsApproximateFloat64())
					memPercent := calculatePercent(float64(totalMem.Value()), float64(allocMem.Value()))
					var currentHostPorts []int32
					if showHostPorts {
						for port := range hostPortsMap {
							currentHostPorts = append(currentHostPorts, port)
						}
						sort.Slice(currentHostPorts, func(i, j int) bool { return currentHostPorts[i] < currentHostPorts[j] })
					}
					results[i] = nodeResult{
						node: node, reqCPU: totalCPU, reqMem: totalMem,
						cpuPercent: cpuPercent, memPercent: memPercent, hostPorts: currentHostPorts,
					}
					klog.V(5).InfoS("Finished processing node", "nodeName", node.Name, "reqCPU", totalCPU.String(), "reqMem", totalMem.String())
				}(i, node)
			}
			wg.Wait()
			klog.V(4).InfoS("All nodes processed")

			sortResults(results, sortBy)
			klog.V(4).InfoS("Results sorted", "sortBy", sortBy)

			for _, res := range results {
				allocCPU := res.node.Status.Allocatable.Cpu()
				allocMem := res.node.Status.Allocatable.Memory()
				row := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%.1f%%\t%.1f%%",
					res.node.Name, formatCPU(*allocCPU), formatMemory(allocMem.Value()),
					formatCPU(res.reqCPU), formatMemory(res.reqMem.Value()),
					res.cpuPercent, res.memPercent)
				if showHostPorts {
					portStrings := make([]string, len(res.hostPorts))
					for i, port := range res.hostPorts {
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

			// Print the summary section
			printAllocationSummary(results, showHostPorts)

			klog.V(4).InfoS("Allocations command finished successfully")
			return nil
		},
	}

	cmd.Flags().StringVar(&sortBy, "sort-by", sortByCPUPercent, fmt.Sprintf("Sort nodes by: %s, %s, or %s", sortByCPUPercent, sortByMemoryPercent, sortByNodeName))
	cmd.Flags().BoolVar(&showHostPorts, "show-host-ports", false, "Show host ports used by containers on each node")
	opts.AddFlags(cmd.Flags())
	return cmd
}

// newUtilizationCmd returns the utilization subcommand.
func newUtilizationCmd() *cobra.Command {
	opts := genericclioptions.NewConfigFlags(true)
	var sortBy string

	cmd := &cobra.Command{
		Use:   "utilization [node-selector]",
		Short: "Show actual node utilization (similar to kubectl top node)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sortBy != sortByCPUPercent && sortBy != sortByMemoryPercent && sortBy != sortByNodeName {
				return fmt.Errorf("invalid --sort-by value. Must be one of: cpu-percent, memory-percent, name")
			}
			klog.V(4).InfoS("Starting utilization command", "selector", args[0], "sortBy", sortBy)
			nodeSelector := args[0]

			config, err := opts.ToRESTConfig()
			if err != nil {
				klog.ErrorS(err, "Failed to build Kubernetes client config")
				return err
			}
			klog.V(5).InfoS("REST config created for utilization")

			clientset, err := kubernetes.NewForConfig(config)
			if err != nil {
				klog.ErrorS(err, "Failed to create Kubernetes clientset")
				return err
			}
			metricsClient, err := metricsclient.NewForConfig(config)
			if err != nil {
				klog.ErrorS(err, "Failed to create metrics client")
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute) // Increased timeout
			defer cancel()

			allNodes, err := getAllNodesWithPagination(ctx, clientset, nodeSelector)
			if err != nil {
				klog.ErrorS(err, "Failed to get all nodes with pagination for utilization", "selector", nodeSelector)
				return err
			}
			if len(allNodes) == 0 {
				klog.InfoS("No nodes found with the given selector for utilization.", "selector", nodeSelector)
				return nil
			}

			klog.V(4).InfoS("Fetching node metrics")
			// Note: NodeMetricses().List does not support pagination in the same way core resources do.
			// It typically returns all metrics in one go. If this becomes an issue for very large clusters,
			// we might need to list nodes first and then get metrics for each node individually (less efficient).
			metricsList, err := metricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
			if err != nil {
				klog.ErrorS(err, "Failed to list node metrics")
				return err
			}
			metricsMap := make(map[string]metricsv.NodeMetrics)
			for _, nm := range metricsList.Items {
				metricsMap[nm.Name] = nm
			}
			klog.V(4).InfoS("Node metrics fetched", "count", len(metricsList.Items))

			results := make([]nodeResult, len(allNodes))
			for i, node := range allNodes {
				allocCPU := node.Status.Allocatable.Cpu()
				allocMem := node.Status.Allocatable.Memory()
				var actCPU, actMem resource.Quantity
				if nm, ok := metricsMap[node.Name]; ok {
					actCPU = nm.Usage[corev1.ResourceCPU]
					actMem = nm.Usage[corev1.ResourceMemory]
				} else {
					klog.V(2).InfoS("Metrics not found for node, assuming zero usage", "nodeName", node.Name)
					actCPU = *resource.NewQuantity(0, resource.DecimalSI)
					actMem = *resource.NewQuantity(0, resource.BinarySI)
				}
				results[i] = nodeResult{
					node: node, reqCPU: actCPU, reqMem: actMem,
					cpuPercent: calculatePercent(actCPU.AsApproximateFloat64(), allocCPU.AsApproximateFloat64()),
					memPercent: calculatePercent(float64(actMem.Value()), float64(allocMem.Value())),
				}
			}

			sortResults(results, sortBy)
			klog.V(4).InfoS("Utilization results sorted", "sortBy", sortBy)

			tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
			fmt.Fprintln(tw, "NODE\tCPU\tMEM\tCPU USED\tMEM USED\tCPU USE%\tMEM USE%")
			for _, res := range results {
				allocCPU := res.node.Status.Allocatable.Cpu()
				allocMem := res.node.Status.Allocatable.Memory()
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%.1f%%\t%.1f%%\n",
					res.node.Name, formatCPU(*allocCPU), formatMemory(allocMem.Value()),
					formatCPU(res.reqCPU), formatMemory(res.reqMem.Value()),
					res.cpuPercent, res.memPercent)
			}
			tw.Flush()
			klog.V(4).InfoS("Utilization command finished successfully")
			return nil
		},
	}

	cmd.Flags().StringVar(&sortBy, "sort-by", sortByCPUPercent, fmt.Sprintf("Sort nodes by: %s, %s, or %s", sortByCPUPercent, sortByMemoryPercent, sortByNodeName))
	opts.AddFlags(cmd.Flags())
	return cmd
}

func sortResults(results []nodeResult, sortBy string) {
	sort.Slice(results, func(i, j int) bool {
		switch sortBy {
		case sortByCPUPercent:
			if results[i].cpuPercent != results[j].cpuPercent {
				return results[i].cpuPercent > results[j].cpuPercent
			}
			if results[i].memPercent != results[j].memPercent {
				return results[i].memPercent > results[j].memPercent
			}
			return results[i].node.Name < results[j].node.Name
		case sortByMemoryPercent:
			if results[i].memPercent != results[j].memPercent {
				return results[i].memPercent > results[j].memPercent
			}
			if results[i].cpuPercent != results[j].cpuPercent {
				return results[i].cpuPercent > results[j].cpuPercent
			}
			return results[i].node.Name < results[j].node.Name
		default: // sortByNodeName
			return results[i].node.Name < results[j].node.Name
		}
	})
}

func calculatePercent(used, total float64) float64 {
	if total == 0 {
		return 0
	}
	return (used / total) * 100
}

func aggregatePodRequests(pod *corev1.Pod) (resource.Quantity, resource.Quantity) {
	sumCPU := *resource.NewQuantity(0, resource.DecimalSI)
	sumMem := *resource.NewQuantity(0, resource.BinarySI)
	maxInitCPU := *resource.NewQuantity(0, resource.DecimalSI)
	maxInitMem := *resource.NewQuantity(0, resource.BinarySI)
	for _, container := range pod.Spec.Containers {
		if req, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
			sumCPU.Add(req)
		}
		if req, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
			sumMem.Add(req)
		}
	}
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
	if sumCPU.Cmp(maxInitCPU) < 0 {
		sumCPU = maxInitCPU.DeepCopy()
	}
	if sumMem.Cmp(maxInitMem) < 0 {
		sumMem = maxInitMem.DeepCopy()
	}
	return sumCPU, sumMem
}

// printAllocationSummary prints the summary section for resource allocations.
func printAllocationSummary(results []nodeResult, showHostPorts bool) {
	if len(results) == 0 {
		return // No data to summarize
	}

	fmt.Println("\n--- Node Resource Allocation Summary ---")
	fmt.Printf("Total Nodes: %d\n", len(results))

	printResourcePercentiles(results, "CPU")
	printResourcePercentiles(results, "Memory")

	if showHostPorts {
		printTopHostPorts(results)
	}
}

// printResourcePercentiles calculates and prints percentiles for a given resource (CPU or Memory).
func printResourcePercentiles(results []nodeResult, resourceName string) {
	// Create a copy to sort independently
	sortedResults := make([]nodeResult, len(results))
	copy(sortedResults, results)

	if resourceName == "CPU" {
		sort.Slice(sortedResults, func(i, j int) bool {
			return sortedResults[i].cpuPercent < sortedResults[j].cpuPercent
		})
		fmt.Printf("\nCPU Allocation Percentiles (based on %% of allocatable CPU requested):\n")
	} else { // Memory
		sort.Slice(sortedResults, func(i, j int) bool {
			return sortedResults[i].memPercent < sortedResults[j].memPercent
		})
		fmt.Printf("\nMemory Allocation Percentiles (based on %% of allocatable Memory requested):\n")
	}

	percentiles := []struct {
		name  string
		value float64
	}{
		{"P10", 0.10},
		{"Median(P50)", 0.50},
		{"P90", 0.90},
		{"P99", 0.99},
		{"Max (P100)", 1.00}, // Max is effectively P100
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
		if resourceName == "CPU" {
			fmt.Printf("  - %-12s: %s (Requests: %s, %.1f%%)\n",
				p.name, nodeRes.node.Name, formatCPU(nodeRes.reqCPU), nodeRes.cpuPercent)
		} else { // Memory
			fmt.Printf("  - %-12s: %s (Requests: %s, %.1f%%)\n",
				p.name, nodeRes.node.Name, formatMemory(nodeRes.reqMem.Value()), nodeRes.memPercent)
		}
	}
}

// printTopHostPorts aggregates host port usage and prints the top 10.
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

func formatCPU(q resource.Quantity) string {
	cores := q.AsApproximateFloat64()
	if cores > 0 && cores < 0.1 {
		// For very small non-zero values, show them as 0.1 to avoid "0.0" for actual requests.
		// This behavior can be adjusted based on desired precision for sub-core requests.
		// If a value is truly zero, AsApproximateFloat64() will return 0.
		// The original check `cores > 0 && cores < 0.1` implies we want to show at least 0.1
		// if there's *any* request, however small.
		// Let's refine this: if it's truly zero, it should be 0.0. If it's >0 and <0.05, round to 0.1.
		// The current `%.1f` formatting will handle rounding.
		// The original logic `if cores > 0 && cores < 0.1 { cores = 0.1 }`
		// effectively sets a minimum display of 0.1 for any non-zero request less than 0.1.
		// This seems reasonable for readability.
	}
	return fmt.Sprintf("%.1f", cores) // %.1f will round e.g. 0.04 to 0.0, 0.05 to 0.1
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
