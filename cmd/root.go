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

// Package cmd implements the subcommands for the kubectl node-resource plugin.
package cmd

import (
	"flag"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// rootCmd sets up the command structure, persistent flags, and subcommands.
func NewRootCmd(streams genericclioptions.IOStreams) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "kubectl node-resource",
		Short: "A kubectl plugin to show node resource allocation and utilization",
		Long: `"kubectl node-resource" provides insights into Kubernetes node resource
allocation (based on pod requests) and actual utilization (based on
metrics-server data).

It helps administrators and developers understand how resources are being
consumed across their cluster's nodes and node pools.

Examples:
  # Show resource allocation for all nodes
  kubectl node-resource allocation [NODE_SELECTOR]

  # Show resource utilization for a specific node
  kubectl node-resource utilization [NODE_SELECTOR]`,
	}

	// Disable the default completion command
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	// Disable the help command
	rootCmd.SetHelpCommand(&cobra.Command{
		Use:    "no-help",
		Hidden: true,
	})

	// Add klog flags from the global flag.CommandLine to the root command's persistent flags.
	// This makes them available to all subcommands.
	rootCmd.PersistentFlags().AddGoFlagSet(flag.CommandLine)

	// Add subcommands
	rootCmd.AddCommand(newAllocationCmd(streams))
	rootCmd.AddCommand(newUtilizationCmd(streams))

	return rootCmd
}
