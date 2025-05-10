package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
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

func main() {
	// override the default klog flags so that they don't appear in output
	flag.Set("logtostderr", "true")

	root := &cobra.Command{
		Use:   "kubectl-node-resources",
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
	cmd := &cobra.Command{
		Use:   "allocations [node-selector]",
		Short: "Show resource allocations for nodes (sum of pod resource requests)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			fmt.Fprintln(tw, "NODE\tAlloc_CPU\tAlloc_MEM\tReq_CPU\tReq_MEM\t%CPU\t%MEM")

			// Worker pool to concurrently list pods per node, limit max concurrent workers to 20
			var wg sync.WaitGroup
			type nodeResult struct {
				node           corev1.Node
				reqCPU, reqMem resource.Quantity
			}
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
					results[i] = nodeResult{
						node:   node,
						reqCPU: totalCPU,
						reqMem: totalMem,
					}
				}(i, node)
			}
			wg.Wait()

			// Print table rows for allocations
			for _, res := range results {
				allocCPU := res.node.Status.Allocatable.Cpu()
				allocMem := res.node.Status.Allocatable.Memory()

				// get percentage (if allocatable is non-zero)
				percentCPU := computePercent(res.reqCPU, *allocCPU)
				percentMem := computePercent(res.reqMem, *allocMem)

				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					res.node.Name,
					allocCPU.String(),
					allocMem.String(),
					res.reqCPU.String(),
					res.reqMem.String(),
					percentCPU,
					percentMem,
				)
			}
			tw.Flush()
			return nil
		},
	}
	// Add genericclioptions flags to the command
	opts.AddFlags(cmd.Flags())
	return cmd
}

// newUtilizationCmd returns the utilization subcommand.
func newUtilizationCmd() *cobra.Command {
	opts := genericclioptions.NewConfigFlags(true)
	cmd := &cobra.Command{
		Use:   "utilization [node-selector]",
		Short: "Show actual node utilization (similar to kubectl top node)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeSelector := args[0]

			// Build Kubernetes client config
			config, err := opts.ToRESTConfig()
			if err != nil {
				return err
			}
			// Metrics client configuration; note that QPS/Burst are less critical for metrics.
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

			// Prepare table writer
			tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
			fmt.Fprintln(tw, "NODE\tAlloc_CPU\tAlloc_MEM\tAct_CPU\tAct_MEM\t%CPU\t%MEM")
			// Iterate over nodes and print metrics
			for _, node := range nodeList.Items {
				allocCPU := node.Status.Allocatable.Cpu()
				allocMem := node.Status.Allocatable.Memory()
				// Get actual metrics if available
				var actCPU, actMem resource.Quantity
				if nm, ok := metricsMap[node.Name]; ok {
					actCPU = nm.Usage[corev1.ResourceCPU]
					actMem = nm.Usage[corev1.ResourceMemory]
				} else {
					actCPU = *resource.NewQuantity(0, resource.DecimalSI)
					actMem = *resource.NewQuantity(0, resource.BinarySI)
				}
				percentCPU := computePercent(actCPU, *allocCPU)
				percentMem := computePercent(actMem, *allocMem)
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					node.Name,
					allocCPU.String(),
					allocMem.String(),
					actCPU.String(),
					actMem.String(),
					percentCPU,
					percentMem,
				)
			}
			tw.Flush()
			return nil
		},
	}
	opts.AddFlags(cmd.Flags())
	return cmd
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

// computePercent returns the percentage of used relative to allocatable.
func computePercent(used, alloc resource.Quantity) string {
	allocFloat, _ := strconv.ParseFloat(alloc.String(), 64)
	usedFloat, _ := strconv.ParseFloat(used.String(), 64)
	if allocFloat == 0 {
		return "N/A"
	}
	percent := (usedFloat / allocFloat) * 100
	return fmt.Sprintf("%.0f%%", math.Round(percent))
}
