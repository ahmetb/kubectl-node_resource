package output

// We will refer to NodeResult from the utils package.
// If it's not there, we'll address it. For now, assume it will be.
// "kubectl-node_resources/pkg/utils"
// corev1 "k8s.io/api/core/v1"
// "k8s.io/apimachinery/pkg/api/resource"

// JSONNode represents a single node's data in JSON format.
// Fields are tagged with `omitempty` if they are not always present.
type JSONNode struct {
	Name              string  `json:"name"`
	CPUAllocatable    string  `json:"cpuAllocatable"`
	CPURequested      string  `json:"cpuRequested,omitempty"` // For allocation
	CPUUsed           string  `json:"cpuUsed,omitempty"`      // For utilization
	CPUPercent        float64 `json:"cpuPercent"`
	MemoryAllocatable string  `json:"memoryAllocatable"`
	MemoryRequested   string  `json:"memoryRequested,omitempty"` // For allocation
	MemoryUsed        string  `json:"memoryUsed,omitempty"`      // For utilization
	MemoryPercent     float64 `json:"memoryPercent"`
	FreeCPU           string  `json:"freeCPU,omitempty"`    // If showFree is true
	FreeMemory        string  `json:"freeMemory,omitempty"` // If showFree is true
	HostPorts         []int32 `json:"hostPorts,omitempty"`  // For allocation, if showHostPorts is true
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

// JSONSummary represents the summary section of the JSON output.
type JSONSummary struct {
	TotalNodes        int                    `json:"totalNodes"`
	CPUPercentiles    []JSONPercentileDetail `json:"cpuPercentiles,omitempty"`
	MemoryPercentiles []JSONPercentileDetail `json:"memoryPercentiles,omitempty"`
	TopHostPorts      []JSONHostPortSummary  `json:"topHostPorts,omitempty"` // For allocation summary
}

// JSONOutput is the root structure for the JSON output.
type JSONOutput struct {
	Nodes   []JSONNode   `json:"nodes,omitempty"`   // Omit if summaryOnly is true and nodes are not needed
	Summary *JSONSummary `json:"summary,omitempty"` // Pointer to allow omitting the summary if not requested
}
