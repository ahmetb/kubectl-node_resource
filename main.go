package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	// "sync" // Replaced by errgroup for this section
	"text/tabwriter"
	"time"

	"golang.org/x/sync/errgroup"
	// "sync/atomic" // No longer directly needed here, moved to progressbar_utils
	"github.com/spf13/cobra"
	// "github.com/mattn/go-isatty" // Moved to progressbar_utils
	// "github.com/schollz/progressbar/v3" // Moved to progressbar_utils
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

			results := make([]nodeResult, len(allNodes))
			progressHelper := newProgressBarHelper(len(allNodes)) // Initialize progress bar helper

			// Use errgroup for concurrent processing
			g, gCtx := errgroup.WithContext(ctx)
			g.SetLimit(20) // Concurrency limiter for k8s API calls

			klog.V(4).InfoS("Processing nodes in parallel with errgroup", "maxWorkers", 20, "nodeCount", len(allNodes))
			for i, node := range allNodes {
				i, node := i, node // Capture range variables
				g.Go(func() error {
					klog.V(5).InfoS("Processing node", "nodeName", node.Name)

					// Use gCtx for API calls so they can be cancelled by the errgroup
					podList, err := clientset.CoreV1().Pods("").List(gCtx, metav1.ListOptions{
						FieldSelector:   "spec.nodeName=" + node.Name,
						ResourceVersion: "0",
						Limit:           1000, // Limit pods per node call
					})
					if err != nil {
						// No klog.ErrorS here, just return the error to the group
						return fmt.Errorf("failed to list pods for node %s: %w", node.Name, err)
					}
					klog.V(5).InfoS("Pods listed for node", "nodeName", node.Name, "podCount", len(podList.Items))

					var totalCPU, totalMem resource.Quantity
					hostPortsMap := make(map[int32]struct{})

					for _, pod := range podList.Items {
						// Check for context cancellation before processing each pod
						if gCtx.Err() != nil {
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
					results[i] = nodeResult{ // This write needs to be thread-safe if results is shared directly, but here each goroutine writes to its own index.
						node: node, reqCPU: totalCPU, reqMem: totalMem,
						cpuPercent: cpuPercent, memPercent: memPercent, hostPorts: currentHostPorts,
					}
					klog.V(5).InfoS("Finished processing node", "nodeName", node.Name, "reqCPU", totalCPU.String(), "reqMem", totalMem.String())

					if progressHelper != nil {
						progressHelper.Increment()
					}
					return nil
				})
			}

			// Wait for all goroutines to complete and check for errors
			if err := g.Wait(); err != nil {
				if progressHelper != nil {
					progressHelper.Finish() // Ensure progress bar is cleaned up on error
				}
				klog.ErrorS(err, "Error processing nodes")
				return err // Propagate the error
			}

			if progressHelper != nil {
				progressHelper.Finish()
			}
			klog.V(4).InfoS("All nodes processed successfully")

			sortResults(results, sortBy)
			klog.V(4).InfoS("Results sorted", "sortBy", sortBy)

			for _, res := range results {
				allocCPU := res.node.Status.Allocatable.Cpu()
				allocMem := res.node.Status.Allocatable.Memory()
				row := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s%.1f%%%s\t%s%.1f%%%s",
					res.node.Name, formatCPU(*allocCPU), formatMemory(allocMem.Value()),
					formatCPU(res.reqCPU), formatMemory(res.reqMem.Value()),
					GetColorForPercentage(res.cpuPercent), res.cpuPercent, colorReset,
					GetColorForPercentage(res.memPercent), res.memPercent, colorReset)
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
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s%.1f%%%s\t%s%.1f%%%s\n",
					res.node.Name, formatCPU(*allocCPU), formatMemory(allocMem.Value()),
					formatCPU(res.reqCPU), formatMemory(res.reqMem.Value()),
					GetColorForPercentage(res.cpuPercent), res.cpuPercent, colorReset,
					GetColorForPercentage(res.memPercent), res.memPercent, colorReset)
			}
			tw.Flush()

			// Print the utilization summary section
			printUtilizationSummary(results)

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
