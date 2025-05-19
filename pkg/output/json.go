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

import (
	"encoding/json"
	"fmt"
	"kubectl-node_resources/pkg/options" // Changed
	"kubectl-node_resources/pkg/utils"   // For NodeResult and formatting functions

	// For io.Writer, though genericclioptions.IOStreams is better
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/klog/v2"
)

// ToJSONNode converts a utils.NodeResult to a JSONNode.
// cmdType helps determine whether to populate Requested or Used fields.
// displayOpts control optional fields.
func ToJSONNode(nodeRes utils.NodeResult, cmdType utils.CmdType, displayOpts options.DisplayOptions) JSONNode {
	jsonNode := JSONNode{
		Name: nodeRes.Node.Name,
	}

	if displayOpts.ShowCPU {
		jsonNode.CPUAllocatable = utils.FormatCPU(*nodeRes.Node.Status.Allocatable.Cpu())
		jsonNode.CPUPercent = nodeRes.CPUPercent
	}
	if displayOpts.ShowMemory {
		jsonNode.MemoryAllocatable = utils.FormatMemory(nodeRes.Node.Status.Allocatable.Memory().Value())
		jsonNode.MemoryPercent = nodeRes.MemPercent
	}

	if cmdType == utils.CmdTypeAllocation {
		if displayOpts.ShowCPU {
			jsonNode.CPURequested = utils.FormatCPU(nodeRes.ReqCPU)
		}
		if displayOpts.ShowMemory {
			jsonNode.MemoryRequested = utils.FormatMemory(nodeRes.ReqMem.Value())
		}
		if displayOpts.ShowHostPorts {
			if nodeRes.HostPorts == nil {
				jsonNode.HostPorts = []int32{}
			} else {
				jsonNode.HostPorts = nodeRes.HostPorts
			}
		}
		if displayOpts.ShowEphemeralStorage {
			jsonNode.EphemeralStorageAllocatable = utils.FormatMemory(nodeRes.AllocEphemeralStorage.Value())
			jsonNode.EphemeralStorageRequested = utils.FormatMemory(nodeRes.ReqEphemeralStorage.Value())
			jsonNode.EphemeralStoragePercent = nodeRes.EphemeralStoragePercent
		}
		if displayOpts.ShowGPU {
			jsonNode.GPUAllocatable = utils.FormatGPU(nodeRes.AllocGPU)
			jsonNode.GPURequested = utils.FormatGPU(nodeRes.ReqGPU)
			jsonNode.GPUPercent = nodeRes.GPUPercent
		}
		if displayOpts.ShowTaints {
			for _, taint := range nodeRes.Taints {
				jsonNode.Taints = append(jsonNode.Taints, taint.ToString())
			}
		}
	} else if cmdType == utils.CmdTypeUtilization {
		if displayOpts.ShowCPU {
			jsonNode.CPUUsed = utils.FormatCPU(nodeRes.ReqCPU) // ReqCPU stores actual usage in utilization context
		}
		if displayOpts.ShowMemory {
			jsonNode.MemoryUsed = utils.FormatMemory(nodeRes.ReqMem.Value()) // ReqMem stores actual usage
		}
		// Note: Ephemeral storage utilization is not typically shown directly by 'kubectl top node'
		// but if displayOpts.ShowEphemeralStorage is true, we can include it.
		if displayOpts.ShowEphemeralStorage {
			jsonNode.EphemeralStorageAllocatable = utils.FormatMemory(nodeRes.AllocEphemeralStorage.Value())
			// Assuming ReqEphemeralStorage holds utilization data if available for utilization command
			jsonNode.EphemeralStorageUsed = utils.FormatMemory(nodeRes.ReqEphemeralStorage.Value())
			jsonNode.EphemeralStoragePercent = nodeRes.EphemeralStoragePercent
		}
		if displayOpts.ShowGPU {
			jsonNode.GPUAllocatable = utils.FormatGPU(nodeRes.AllocGPU) // Allocatable is still relevant
			jsonNode.GPUUsed = utils.FormatGPU(nodeRes.ReqGPU)          // ReqGPU stores actual usage in utilization context for GPU
			jsonNode.GPUPercent = nodeRes.GPUPercent
		}
	}

	if displayOpts.ShowFree {
		if displayOpts.ShowCPU {
			jsonNode.FreeCPU = utils.FormatCPU(nodeRes.FreeCPU)
		}
		if displayOpts.ShowMemory {
			jsonNode.FreeMemory = utils.FormatMemory(nodeRes.FreeMem.Value())
		}
		if displayOpts.ShowEphemeralStorage { // Assuming FreeEphemeralStorage only makes sense if EphemeralStorage is shown
			jsonNode.FreeEphemeralStorage = utils.FormatMemory(nodeRes.FreeEphemeralStorage.Value())
		}
		if displayOpts.ShowGPU { // Assuming FreeGPU only makes sense if GPU is shown
			jsonNode.FreeGPU = utils.FormatGPU(nodeRes.FreeGPU)
		}
	}
	return jsonNode
}

// PrintJSON marshals the JSONOutput structure and prints it to the provided stream.
func PrintJSON(data JSONOutput, streams genericclioptions.IOStreams) error {
	jsonData, err := json.MarshalIndent(data, "", "  ") // Using "  " for indentation
	if err != nil {
		klog.ErrorS(err, "Failed to marshal data to JSON")
		return fmt.Errorf("failed to marshal data to JSON: %w", err)
	}

	fmt.Fprintln(streams.Out, string(jsonData))
	return nil
}

// GetJSONOutput prepares the full JSONOutput structure.
// It takes the list of results, command type, and display options.
func GetJSONOutput(
	results []utils.NodeResult,
	cmdType utils.CmdType,
	displayOpts options.DisplayOptions, // Changed
	summaryOpt string, // To decide if summary is needed
	// The summary func will also need to be updated to accept options.DisplayOptions
	getSummaryFunc func([]utils.NodeResult, options.DisplayOptions, utils.CmdType) (*JSONSummary, error),
) (JSONOutput, error) {
	output := JSONOutput{}

	// Populate nodes unless summaryOnly is true
	if summaryOpt != utils.SummaryOnly {
		jsonNodes := make([]JSONNode, len(results))
		for i, res := range results {
			// Pass the full displayOpts to ToJSONNode.
			// ToJSONNode will internally handle which fields are relevant based on cmdType if necessary,
			// though for JSON, showing all requested fields if true is generally fine.
			currentDisplayOpts := displayOpts
			if cmdType == utils.CmdTypeUtilization {
				// For utilization, hostPorts and ephemeralStorage might not be applicable from metrics-server
				// However, if the flags were somehow true, ToJSONNode can handle it.
				// We ensure that ToJSONNode correctly interprets these for utilization.
				// The DisplayOptions struct itself doesn't change, ToJSONNode adapts.
			}
			jsonNodes[i] = ToJSONNode(res, cmdType, currentDisplayOpts)
		}
		output.Nodes = jsonNodes
	}

	// Populate summary if not hidden
	if summaryOpt != utils.SummaryHide {
		if getSummaryFunc != nil {
			// Pass displayOpts to the summary function.
			// The summary function will need to be updated to accept cmd.DisplayOptions.
			summaryData, err := getSummaryFunc(results, displayOpts, cmdType)
			if err != nil {
				return JSONOutput{}, fmt.Errorf("failed to get summary data for JSON output: %w", err)
			}
			output.Summary = summaryData
		}
	}
	return output, nil
}
