package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metricsv "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
)

type nodeResult struct {
	node       corev1.Node
	reqCPU     resource.Quantity
	reqMem     resource.Quantity
	cpuPercent float64
	memPercent float64
}

const (
	sortByCPUPercent    = "cpu-percent"
	sortByMemoryPercent = "memory-percent"
	sortByNodeName      = "name"
)

func main() {
	// override the default klog flags so that they don't appear in output
	flag.Set("logtostderr", "true")

	root := &cobra.Command{
		Use:   "kubectl node-resources",
		Short: "A kubectl plugin to show node resource allocations and utilization",
	}

	root.AddCommand(newAllocationsCmd())
	root.AddCommand(newUtilizationCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// newAllocationsCmd returns the allocations subcommand.
func newAllocationsCmd() *cobra.Command {
	opts := genericclioptions.NewConfigFlags(true)
	var sortBy string

	cmd := &cobra.Command{
		Use:   "allocations [node-selector]",
		Short: "Show resource allocations for nodes (sum of pod resource requests)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sortBy != sortByCPUPercent && sortBy != sortByMemoryPercent && sortBy != sortByNodeName {
				return fmt.Errorf("invalid --sort-by value. Must be one of: cpu-percent, memory-percent, node-name")
			}

			nodeSelector := args[0]

			// Build Kubernetes client config
			config, err := opts.ToRESTConfig()
			if err != nil {
				return err
			}
			// Disable client QPS / burst
			config.QPS = -1
			config.Burst = -1

			clientset, err := kubernetes.NewForConfig(config)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// List nodes using the provided selector
			nodeList, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{
				LabelSelector: nodeSelector,
			})
			if err != nil {
				return err
			}
			if len(nodeList.Items) == 0 {
				fmt.Println("No nodes found with the given selector.")
				return nil
			}

			// Prepare table writer
			tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
			fmt.Fprintln(tw, "NODE\tCPU\tMEM\tCPU REQ\tMEM REQ\tCPU%\tMEM%")

			// Worker pool to concurrently list pods per node, limit max concurrent workers to 20
			var wg sync.WaitGroup
			results := make([]nodeResult, len(nodeList.Items))
			sem := make(chan struct{}, 20)
			for i, node := range nodeList.Items {
				wg.Add(1)
				sem <- struct{}{}
				go func(i int, node corev1.Node) {
					defer wg.Done()
					defer func() { <-sem }()

					// List pods scheduled on this node across all namespaces.
					podList, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
						FieldSelector:   "spec.nodeName=" + node.Name,
						ResourceVersion: "0",
					})
					if err != nil {
						// On error, skip listing resources and assume zero requests.
						results[i] = nodeResult{node: node}
						return
					}

					// Sum resource requests across pods.
					var totalCPU, totalMem resource.Quantity
					for _, pod := range podList.Items {
						podCPU, podMem := aggregatePodRequests(&pod)
						totalCPU.Add(podCPU)
						totalMem.Add(podMem)
					}

					allocCPU := node.Status.Allocatable.Cpu()
					allocMem := node.Status.Allocatable.Memory()
					cpuPercent := calculatePercent(totalCPU.AsApproximateFloat64(), allocCPU.AsApproximateFloat64())
					memPercent := calculatePercent(float64(totalMem.Value()), float64(allocMem.Value()))

					results[i] = nodeResult{
						node:       node,
						reqCPU:     totalCPU,
						reqMem:     totalMem,
						cpuPercent: cpuPercent,
						memPercent: memPercent,
					}
				}(i, node)
			}
			wg.Wait()

			// Sort results
			sortResults(results, sortBy)

			// Print table rows for allocations
			for _, res := range results {
				allocCPU := res.node.Status.Allocatable.Cpu()
				allocMem := res.node.Status.Allocatable.Memory()

				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%.1f%%\t%.1f%%\n",
					res.node.Name,
					formatCPU(*allocCPU),
					formatMemory(allocMem.Value()),
					formatCPU(res.reqCPU),
					formatMemory(res.reqMem.Value()),
					res.cpuPercent,
					res.memPercent,
				)
			}
			tw.Flush()
			return nil
		},
	}

	cmd.Flags().StringVar(&sortBy, "sort-by", sortByCPUPercent,
		fmt.Sprintf("Sort nodes by: %s, %s, or %s", sortByCPUPercent, sortByMemoryPercent, sortByNodeName))

	// Add genericclioptions flags to the command
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

			nodeSelector := args[0]

			// Build Kubernetes client config
			config, err := opts.ToRESTConfig()
			if err != nil {
				return err
			}
			clientset, err := kubernetes.NewForConfig(config)
			if err != nil {
				return err
			}
			metricsClient, err := metricsclient.NewForConfig(config)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// List nodes using the provided selector
			nodeList, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{
				LabelSelector: nodeSelector,
			})
			if err != nil {
				return err
			}
			if len(nodeList.Items) == 0 {
				fmt.Println("No nodes found with the given selector.")
				return nil
			}

			// Get node metrics
			metricsList, err := metricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
			if err != nil {
				return err
			}
			// Create a map from node name to metrics.
			metricsMap := make(map[string]metricsv.NodeMetrics)
			for _, nm := range metricsList.Items {
				metricsMap[nm.Name] = nm
			}

			// Prepare results slice for sorting
			results := make([]nodeResult, len(nodeList.Items))
			for i, node := range nodeList.Items {
				allocCPU := node.Status.Allocatable.Cpu()
				allocMem := node.Status.Allocatable.Memory()
				var actCPU, actMem resource.Quantity
				if nm, ok := metricsMap[node.Name]; ok {
					actCPU = nm.Usage[corev1.ResourceCPU]
					actMem = nm.Usage[corev1.ResourceMemory]
				} else {
					actCPU = *resource.NewQuantity(0, resource.DecimalSI)
					actMem = *resource.NewQuantity(0, resource.BinarySI)
				}

				results[i] = nodeResult{
					node:       node,
					reqCPU:     actCPU,
					reqMem:     actMem,
					cpuPercent: calculatePercent(actCPU.AsApproximateFloat64(), allocCPU.AsApproximateFloat64()),
					memPercent: calculatePercent(float64(actMem.Value()), float64(allocMem.Value())),
				}
			}

			// Sort results
			sortResults(results, sortBy)

			// Prepare table writer and print results
			tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
			fmt.Fprintln(tw, "NODE\tCPU\tMEM\tCPU USED\tMEM USED\tCPU USE%\tMEM USE%")

			for _, res := range results {
				allocCPU := res.node.Status.Allocatable.Cpu()
				allocMem := res.node.Status.Allocatable.Memory()

				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%.1f%%\t%.1f%%\n",
					res.node.Name,
					formatCPU(*allocCPU),
					formatMemory(allocMem.Value()),
					formatCPU(res.reqCPU),
					formatMemory(res.reqMem.Value()),
					res.cpuPercent,
					res.memPercent,
				)
			}
			tw.Flush()
			return nil
		},
	}

	cmd.Flags().StringVar(&sortBy, "sort-by", sortByCPUPercent,
		fmt.Sprintf("Sort nodes by: %s, %s, or %s", sortByCPUPercent, sortByMemoryPercent, sortByNodeName))

	opts.AddFlags(cmd.Flags())
	return cmd
}

// sortResults sorts the results slice based on the specified sort key
func sortResults(results []nodeResult, sortBy string) {
	sort.Slice(results, func(i, j int) bool {
		switch sortBy {
		case sortByCPUPercent:
			if results[i].cpuPercent != results[j].cpuPercent {
				return results[i].cpuPercent > results[j].cpuPercent // descending
			}
			if results[i].memPercent != results[j].memPercent {
				return results[i].memPercent > results[j].memPercent // descending
			}
			return results[i].node.Name < results[j].node.Name // ascending

		case sortByMemoryPercent:
			if results[i].memPercent != results[j].memPercent {
				return results[i].memPercent > results[j].memPercent // descending
			}
			if results[i].cpuPercent != results[j].cpuPercent {
				return results[i].cpuPercent > results[j].cpuPercent // descending
			}
			return results[i].node.Name < results[j].node.Name // ascending

		default: // sortByNodeName
			return results[i].node.Name < results[j].node.Name // ascending
		}
	})
}

// calculatePercent returns the percentage value
func calculatePercent(used, total float64) float64 {
	if total == 0 {
		return 0
	}
	return (used / total) * 100
}

// aggregatePodRequests sums resource requests for a pod, considering both containers and initContainers.
// For initContainers, the effective request is the maximum request among them.
func aggregatePodRequests(pod *corev1.Pod) (resource.Quantity, resource.Quantity) {
	sumCPU := *resource.NewQuantity(0, resource.DecimalSI)
	sumMem := *resource.NewQuantity(0, resource.BinarySI)
	maxInitCPU := *resource.NewQuantity(0, resource.DecimalSI)
	maxInitMem := *resource.NewQuantity(0, resource.BinarySI)

	// Sum requests for regular containers.
	for _, container := range pod.Spec.Containers {
		if req, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
			sumCPU.Add(req)
		}
		if req, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
			sumMem.Add(req)
		}
	}

	// For initContainers, take the maximum request instead of sum.
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

	// Effective pod CPU/memory is the max of (sum of containers) and (max init container)
	if sumCPU.Cmp(maxInitCPU) < 0 {
		sumCPU = maxInitCPU.DeepCopy()
	}
	if sumMem.Cmp(maxInitMem) < 0 {
		sumMem = maxInitMem.DeepCopy()
	}
	return sumCPU, sumMem
}

// formatCPU formats CPU quantities in decimal cores, with a minimum of 0.1 cores
func formatCPU(q resource.Quantity) string {
	cores := q.AsApproximateFloat64()
	if cores > 0 && cores < 0.1 {
		cores = 0.1
	}
	return fmt.Sprintf("%.1f", cores)
}

// formatMemory formats memory quantities in appropriate units (MiB/GiB)
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
