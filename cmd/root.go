// Package cmd implements the subcommands for the kubectl-node-resources plugin.
package cmd

import (
	"flag"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	// "k8s.io/klog/v2" // No longer needed after removing PersistentPreRunE
)

// NewRootCmd creates the root command for kubectl-node-resources.
// It sets up the command structure, persistent flags, and subcommands.
func NewRootCmd(streams genericclioptions.IOStreams) *cobra.Command {
	// klog flags are initialized in main.go and added to flag.CommandLine

	rootCmd := &cobra.Command{
		Use:   "kubectl-node-resources",
		Short: "A kubectl plugin to show node resource allocations and utilization",
		Long: `kubectl-node-resources is a plugin for kubectl that provides insights
into Kubernetes node resource allocations (based on pod requests) and
actual utilization (based on metrics-server data).

It helps administrators and developers understand how resources are being
consumed across their cluster nodes.`,
		// PersistentPreRunE removed as klog initialization is now in main.go
		// and we are not forcing logtostderr.
	}

	// Add klog flags from the global flag.CommandLine to the root command's persistent flags.
	// This makes them available to all subcommands.
	rootCmd.PersistentFlags().AddGoFlagSet(flag.CommandLine)

	// Add subcommands
	rootCmd.AddCommand(newAllocationsCmd(streams))
	rootCmd.AddCommand(newUtilizationCmd(streams))

	return rootCmd
}
