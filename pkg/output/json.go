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
	"kubectl-node_resources/pkg/utils" // For NodeResult and formatting functions

	// For io.Writer, though genericclioptions.IOStreams is better
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/klog/v2"
)

const (
	CmdTypeAllocation  = "allocation"
	CmdTypeUtilization = "utilization"
)

// ToJSONNode converts a utils.NodeResult to a JSONNode.
// cmdType helps determine whether to populate Requested or Used fields.
// showFree and showHostPorts flags control optional fields.
func ToJSONNode(nodeRes utils.NodeResult, cmdType string, showFree bool, showHostPorts bool) JSONNode {
	jsonNode := JSONNode{
		Name:              nodeRes.Node.Name,
		CPUAllocatable:    utils.FormatCPU(*nodeRes.Node.Status.Allocatable.Cpu()),
		CPUPercent:        nodeRes.CPUPercent,
		MemoryAllocatable: utils.FormatMemory(nodeRes.Node.Status.Allocatable.Memory().Value()),
		MemoryPercent:     nodeRes.MemPercent,
	}

	if cmdType == CmdTypeAllocation {
		jsonNode.CPURequested = utils.FormatCPU(nodeRes.ReqCPU)
		jsonNode.MemoryRequested = utils.FormatMemory(nodeRes.ReqMem.Value())
		if showHostPorts {
			// Ensure HostPorts is not nil to avoid "null" in JSON if empty
			if nodeRes.HostPorts == nil {
				jsonNode.HostPorts = []int32{}
			} else {
				jsonNode.HostPorts = nodeRes.HostPorts
			}
		}
	} else if cmdType == CmdTypeUtilization {
		jsonNode.CPUUsed = utils.FormatCPU(nodeRes.ReqCPU)               // ReqCPU stores actual usage in utilization context
		jsonNode.MemoryUsed = utils.FormatMemory(nodeRes.ReqMem.Value()) // ReqMem stores actual usage
	}

	if showFree {
		jsonNode.FreeCPU = utils.FormatCPU(nodeRes.FreeCPU)
		jsonNode.FreeMemory = utils.FormatMemory(nodeRes.FreeMem.Value())
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
// It takes the list of results, command type, and various display options.
func GetJSONOutput(
	results []utils.NodeResult,
	cmdType string,
	showFree bool,
	showHostPorts bool, // Only relevant for "allocations" command
	summaryOpt string, // To decide if summary is needed
	getSummaryFunc func([]utils.NodeResult, bool, string) (*JSONSummary, error), // Function to get summary data
) (JSONOutput, error) {
	output := JSONOutput{}

	// Populate nodes unless summaryOnly is true
	if summaryOpt != utils.SummaryOnly {
		jsonNodes := make([]JSONNode, len(results))
		for i, res := range results {
			jsonNodes[i] = ToJSONNode(res, cmdType, showFree, showHostPorts && cmdType == CmdTypeAllocation)
		}
		output.Nodes = jsonNodes
	}

	// Populate summary if not hidden
	if summaryOpt != utils.SummaryHide {
		if getSummaryFunc != nil {
			// Pass cmdType to the summary function
			summaryData, err := getSummaryFunc(results, showHostPorts && cmdType == CmdTypeAllocation, cmdType)
			if err != nil {
				return JSONOutput{}, fmt.Errorf("failed to get summary data for JSON output: %w", err)
			}
			output.Summary = summaryData
		}
	}
	return output, nil
}
