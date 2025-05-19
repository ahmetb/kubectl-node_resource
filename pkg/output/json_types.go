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

package output

// We will refer to NodeResult from the utils package.
// If it's not there, we'll address it. For now, assume it will be.
// "kubectl-node_resources/pkg/utils"
// corev1 "k8s.io/api/core/v1"
// "k8s.io/apimachinery/pkg/api/resource"

// JSONNode represents a single node's data in JSON format.
// Fields are tagged with `omitempty` if they are not always present.
type JSONNode struct {
	Name                        string   `json:"name"`
	CPUAllocatable              string   `json:"cpuAllocatable"`
	CPURequested                string   `json:"cpuRequested,omitempty"` // For allocation
	CPUUsed                     string   `json:"cpuUsed,omitempty"`      // For utilization
	CPUPercent                  float64  `json:"cpuPercent"`
	MemoryAllocatable           string   `json:"memoryAllocatable"`
	MemoryRequested             string   `json:"memoryRequested,omitempty"` // For allocation
	MemoryUsed                  string   `json:"memoryUsed,omitempty"`      // For utilization
	MemoryPercent               float64  `json:"memoryPercent"`
	EphemeralStorageAllocatable string   `json:"ephemeralStorageAllocatable,omitempty"`
	EphemeralStorageRequested   string   `json:"ephemeralStorageRequested,omitempty"` // For allocation
	EphemeralStorageUsed        string   `json:"ephemeralStorageUsed,omitempty"`      // For utilization
	EphemeralStoragePercent     float64  `json:"ephemeralStoragePercent,omitempty"`
	FreeCPU                     string   `json:"freeCPU,omitempty"`              // If showFree is true
	FreeMemory                  string   `json:"freeMemory,omitempty"`           // If showFree is true
	FreeEphemeralStorage        string   `json:"freeEphemeralStorage,omitempty"` // If showFree and showEphemeralStorage are true
	GPUAllocatable              string   `json:"gpuAllocatable,omitempty"`
	GPURequested                string   `json:"gpuRequested,omitempty"` // For allocation
	GPUUsed                     string   `json:"gpuUsed,omitempty"`      // For utilization
	GPUPercent                  float64  `json:"gpuPercent,omitempty"`
	FreeGPU                     string   `json:"freeGPU,omitempty"`   // If showFree and showGPU are true
	HostPorts                   []int32  `json:"hostPorts,omitempty"` // For allocation, if showHostPorts is true
	Taints                      []string `json:"taints,omitempty"`    // For allocation, if showTaints is true
}

// JSONPercentileDetail represents a single percentile data point in the summary.
type JSONPercentileDetail struct {
	Percentile string  `json:"percentile"` // e.g., "P50 (median)"
	NodeName   string  `json:"nodeName"`
	Value      string  `json:"value"`   // Formatted resource value (e.g., "1.2" for CPU, "500Mi" for Mem)
	Percent    float64 `json:"percent"` // The actual percentage value
}

// JSONHostPortSummary represents a host port usage summary.
type JSONHostPortSummary struct {
	Port      int32 `json:"port"`
	NodeCount int   `json:"nodeCount"`
}

// JSONTaintsSummary represents a taints usage summary.
type JSONTaintsSummary struct {
	Taint     string `json:"taint"`
	NodeCount int    `json:"nodeCount"`
}

// JSONSummary represents the summary section of the JSON output.
type JSONSummary struct {
	TotalNodes                    int                    `json:"totalNodes"`
	TotalCPUAllocatable           string                 `json:"totalCpuAllocatable"`
	TotalCPURequestedOrUsed       string                 `json:"totalCpuRequestedOrUsed"` // Context-dependent
	AverageCPUPercent             float64                `json:"averageCpuPercent"`
	TotalMemoryAllocatable        string                 `json:"totalMemoryAllocatable"`
	TotalMemoryRequestedOrUsed    string                 `json:"totalMemoryRequestedOrUsed"` // Context-dependent
	AverageMemoryPercent          float64                `json:"averageMemoryPercent"`
	TotalEphemeralAllocatable     string                 `json:"totalEphemeralAllocatable,omitempty"`
	TotalEphemeralRequestedOrUsed string                 `json:"totalEphemeralRequestedOrUsed,omitempty"` // Context-dependent
	AverageEphemeralPercent       float64                `json:"averageEphemeralPercent,omitempty"`
	TotalGPUAllocatable           string                 `json:"totalGpuAllocatable,omitempty"`
	TotalGPURequestedOrUsed       string                 `json:"totalGpuRequestedOrUsed,omitempty"` // Context-dependent
	AverageGPUPercent             float64                `json:"averageGpuPercent,omitempty"`
	CPUPercentiles                []JSONPercentileDetail `json:"cpuPercentiles,omitempty"`    // Keeping existing percentile fields
	MemoryPercentiles             []JSONPercentileDetail `json:"memoryPercentiles,omitempty"` // Keeping existing percentile fields
	EphemeralStoragePercentiles   []JSONPercentileDetail `json:"ephemeralStoragePercentiles,omitempty"`
	GPUPercentiles                []JSONPercentileDetail `json:"gpuPercentiles,omitempty"`
	TopHostPorts                  []JSONHostPortSummary  `json:"topHostPorts,omitempty"`  // For allocation summary
	TaintsSummary                 []JSONTaintsSummary    `json:"taintsSummary,omitempty"` // For allocation summary
}

// JSONOutput is the root structure for the JSON output.
type JSONOutput struct {
	Nodes   []JSONNode   `json:"nodes,omitempty"`   // Omit if summaryOnly is true and nodes are not needed
	Summary *JSONSummary `json:"summary,omitempty"` // Pointer to allow omitting the summary if not requested
}
