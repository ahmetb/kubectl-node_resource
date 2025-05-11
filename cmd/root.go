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
