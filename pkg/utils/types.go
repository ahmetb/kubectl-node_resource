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
	Taints     []corev1.Taint
	FreeCPU    resource.Quantity // Added for --show-free
	FreeMem    resource.Quantity // Added for --show-free

	// Ephemeral Storage fields
	AllocEphemeralStorage   resource.Quantity
	ReqEphemeralStorage     resource.Quantity
	EphemeralStoragePercent float64
	FreeEphemeralStorage    resource.Quantity

	// GPU fields
	AllocGPU   resource.Quantity
	ReqGPU     resource.Quantity
	GPUPercent float64
	FreeGPU    resource.Quantity

	// Pod Count fields
	PodCount        int64
	AllocatablePods resource.Quantity
	PodPercent      float64
}

// CmdType indicates the command type for shared functions
// Assuming these are not duplicates, will verify after reading utils.go
type CmdType string

const (
	CmdTypeAllocation  CmdType = "allocation"
	CmdTypeUtilization CmdType = "utilization"
)
