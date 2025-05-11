package main

import (
	"os"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/klog/v2"

	"kubectl-node_resources/cmd"
)

func main() {
	klog.InitFlags(nil) // Initialize klog flags globally

	streams := genericclioptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr}
	rootCmd := cmd.NewRootCmd(streams)

	if err := rootCmd.Execute(); err != nil {
		// klog.ErrorS is used here as it's the final exit point.
		// The error from Execute() could already be wrapped by fmt.Errorf.
		klog.ErrorS(err, "Error executing command")
		os.Exit(1)
	}
}
