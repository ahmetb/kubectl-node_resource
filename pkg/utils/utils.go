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

// Package utils provides shared utility types and functions for the kubectl node-resource plugin.
package utils

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/pager"
)

const (
	// NodeListLimit is the page limit for node listing.
	NodeListLimit = 500

	// SortByCPUPercent sorts nodes by CPU percentage.
	SortByCPUPercent = "cpu-percent"
	// SortByMemoryPercent sorts nodes by memory percentage.
	SortByMemoryPercent = "memory-percent"
	// SortByNodeName sorts nodes by name.
	SortByNodeName = "name"

	// SummaryShow shows the node list and prints the summary.
	SummaryShow = "show"
	// SummaryOnly only prints the summary.
	SummaryOnly = "only"
	// SummaryHide does not show the node summary.
	SummaryHide = "hide"
)

// GetAllNodesWithPagination retrieves all nodes matching the label selector using pagination.
// It handles potential errors during the pagination process.
func GetAllNodesWithPagination(ctx context.Context, clientset *kubernetes.Clientset, labelSelector string) ([]corev1.Node, error) {
	pg := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		opts.LabelSelector = labelSelector
		opts.Limit = NodeListLimit
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

// FormatCPU formats a CPU quantity (cores) as a string with one decimal place.
func FormatCPU(q resource.Quantity) string {
	cores := q.AsApproximateFloat64()
	return fmt.Sprintf("%.1f", cores)
}

// FormatMemory formats a memory quantity (bytes) into a human-readable string (MiB or GiB).
func FormatMemory(bytes int64) string {
	const (
		mib = 1024 * 1024
		gib = mib * 1024
	)
	switch {
	case bytes >= gib:
		return fmt.Sprintf("%.1fGi", float64(bytes)/float64(gib))
	default:
		return fmt.Sprintf("%dMi", bytes/mib)
	}
}

// CalculatePercent calculates the percentage of used resources against the total.
// It returns 0 if the total is 0 to avoid division by zero.
func CalculatePercent(used, total float64) float64 {
	if total == 0 {
		return 0
	}
	return (used / total) * 100
}

// SortResults sorts a slice of NodeResult based on the sortBy criteria.
// It supports sorting by CPU percentage, memory percentage, or node name.
// Secondary sort criteria are applied for tie-breaking.
func SortResults(results []NodeResult, sortBy string) {
	sort.Slice(results, func(i, j int) bool {
		switch sortBy {
		case SortByCPUPercent:
			if results[i].CPUPercent != results[j].CPUPercent {
				return results[i].CPUPercent > results[j].CPUPercent
			}
			if results[i].MemPercent != results[j].MemPercent {
				return results[i].MemPercent > results[j].MemPercent
			}
			return results[i].Node.Name < results[j].Node.Name
		case SortByMemoryPercent:
			if results[i].MemPercent != results[j].MemPercent {
				return results[i].MemPercent > results[j].MemPercent
			}
			if results[i].CPUPercent != results[j].CPUPercent {
				return results[i].CPUPercent > results[j].CPUPercent
			}
			return results[i].Node.Name < results[j].Node.Name
		default: // SortByNodeName
			return results[i].Node.Name < results[j].Node.Name
		}
	})
}
