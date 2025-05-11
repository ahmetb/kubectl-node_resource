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

package cmd

// DisplayOptions holds the common boolean flags that control what data columns are displayed.
type DisplayOptions struct {
	ShowCPU              bool
	ShowMemory           bool
	ShowFree             bool
	ShowHostPorts        bool // Relevant for allocation command
	ShowEphemeralStorage bool // Relevant for allocation command
	ShowGPU              bool
	GpuResourceKey       string
	JSONOutput           bool
}

// HasPrimaryDataColumns checks if any of the primary data columns (CPU, Memory, EphemeralStorage, HostPorts, GPU)
// are set to be displayed. This is used to determine if there's any content to show in a table.
// ShowFree is not considered a "primary" column on its own as it typically accompanies other columns.
// JSONOutput is an output format, not a column itself.
func (do *DisplayOptions) HasPrimaryDataColumns() bool {
	return do.ShowCPU || do.ShowMemory || do.ShowEphemeralStorage || do.ShowHostPorts || do.ShowGPU
}
