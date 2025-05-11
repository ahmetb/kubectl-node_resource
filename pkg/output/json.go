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

// ToJSONNode converts a utils.NodeResult to a JSONNode.
// cmdType helps determine whether to populate Requested or Used fields.
// showFree, showHostPorts, showEphemeralStorage flags control optional fields.
func ToJSONNode(nodeRes utils.NodeResult, cmdType utils.CmdType, showFree bool, showHostPorts bool, showEphemeralStorage bool) JSONNode {
	jsonNode := JSONNode{
		Name:              nodeRes.Node.Name,
		CPUAllocatable:    utils.FormatCPU(*nodeRes.Node.Status.Allocatable.Cpu()),
		CPUPercent:        nodeRes.CPUPercent,
		MemoryAllocatable: utils.FormatMemory(nodeRes.Node.Status.Allocatable.Memory().Value()),
		MemoryPercent:     nodeRes.MemPercent,
	}

	if cmdType == utils.CmdTypeAllocation {
		jsonNode.CPURequested = utils.FormatCPU(nodeRes.ReqCPU)
		jsonNode.MemoryRequested = utils.FormatMemory(nodeRes.ReqMem.Value())
		if showHostPorts {
			if nodeRes.HostPorts == nil {
				jsonNode.HostPorts = []int32{}
			} else {
				jsonNode.HostPorts = nodeRes.HostPorts
			}
		}
		if showEphemeralStorage {
			jsonNode.EphemeralStorageAllocatable = utils.FormatMemory(nodeRes.AllocEphemeralStorage.Value())
			jsonNode.EphemeralStorageRequested = utils.FormatMemory(nodeRes.ReqEphemeralStorage.Value())
			jsonNode.EphemeralStoragePercent = nodeRes.EphemeralStoragePercent
		}
	} else if cmdType == utils.CmdTypeUtilization {
		jsonNode.CPUUsed = utils.FormatCPU(nodeRes.ReqCPU)               // ReqCPU stores actual usage in utilization context
		jsonNode.MemoryUsed = utils.FormatMemory(nodeRes.ReqMem.Value()) // ReqMem stores actual usage
		if showEphemeralStorage {
			jsonNode.EphemeralStorageAllocatable = utils.FormatMemory(nodeRes.AllocEphemeralStorage.Value())
			jsonNode.EphemeralStorageUsed = utils.FormatMemory(nodeRes.ReqEphemeralStorage.Value()) // ReqEphemeralStorage stores actual usage in util context
			jsonNode.EphemeralStoragePercent = nodeRes.EphemeralStoragePercent
		}
	}

	if showFree {
		jsonNode.FreeCPU = utils.FormatCPU(nodeRes.FreeCPU)
		jsonNode.FreeMemory = utils.FormatMemory(nodeRes.FreeMem.Value())
		if showEphemeralStorage {
			jsonNode.FreeEphemeralStorage = utils.FormatMemory(nodeRes.FreeEphemeralStorage.Value())
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
// It takes the list of results, command type, and various display options.
func GetJSONOutput(
	results []utils.NodeResult,
	cmdType utils.CmdType, // Changed to utils.CmdType
	showFree bool,
	showHostPorts bool, // Only relevant for "allocations" command
	showEphemeralStorage bool, // Added this flag
	summaryOpt string, // To decide if summary is needed
	getSummaryFunc func([]utils.NodeResult, bool, bool, utils.CmdType) (*JSONSummary, error), // Function to get summary data, added showEphemeralStorage & changed cmdType
) (JSONOutput, error) {
	output := JSONOutput{}

	// Populate nodes unless summaryOnly is true
	if summaryOpt != utils.SummaryOnly {
		jsonNodes := make([]JSONNode, len(results))
		for i, res := range results {
			jsonNodes[i] = ToJSONNode(res, cmdType, showFree, showHostPorts && cmdType == utils.CmdTypeAllocation, showEphemeralStorage)
		}
		output.Nodes = jsonNodes
	}

	// Populate summary if not hidden
	if summaryOpt != utils.SummaryHide {
		if getSummaryFunc != nil {
			summaryData, err := getSummaryFunc(results, showHostPorts && cmdType == utils.CmdTypeAllocation, showEphemeralStorage, cmdType)
			if err != nil {
				return JSONOutput{}, fmt.Errorf("failed to get summary data for JSON output: %w", err)
			}
			output.Summary = summaryData
		}
	}
	return output, nil
}
