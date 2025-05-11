// Package utils provides shared utility types and functions for the kubectl node-resource plugin.
package utils

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// NodeResult holds the processed data for a single node, including its resource
// requests/usage and calculated percentages. This struct is used by command runners
// and summary functions.
type NodeResult struct {
	Node       corev1.Node
	ReqCPU     resource.Quantity
	ReqMem     resource.Quantity
	CPUPercent float64
	MemPercent float64
	HostPorts  []int32
	FreeCPU    resource.Quantity // Added for --show-free
	FreeMem    resource.Quantity // Added for --show-free
}
