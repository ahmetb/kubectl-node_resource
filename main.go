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
