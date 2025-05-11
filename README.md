# kubectl node-resource

`kubectl node-resource` is a kubectl plugin that provides insights into
Kubernetes node resource allocation (based on pod requests) and actual
utilization (based on metrics-server data).

It helps administrators and developers understand how resources are being
consumed across their cluster's nodes and node pools.

<!-- Screenshots will be inserted here later -->

## Installation

If you have [Krew](https://krew.sigs.k8s.io/) installed, you can install
`node-resource` with the following command:

```bash
kubectl krew install node-resource
```

## Features

- **Summary View**: Provides a summary view with histograms and distribution
buckets for resource allocation and utilization.
- **Structured JSON Output**: Supports JSON output (with `--json`) for easy
integration with other tools and scripts.
- **Fast Pod Querying**:
Utilizes optimized and parallel pod querying from the API server's watch cache
for the `allocation` command.
- **Color Output**: Uses color-coded output in the terminal to visually indicate
resource pressure on each node.
- **Flexible Sorting and Filtering**: Sort nodes by CPU/memory usage percentage.
- **Free Resource Display**: In addition to showing used resources, it can also display
  the free resources (`--show-free`) on each node.
- **Host Port Display**: Displays host ports used by containers on nodes
  (`--show-host-ports`).

## Usage

This plugin offers two main subcommands:

- [**`utilization`**](#utilization-subcommand): Show pod resource allocations on nodes
- [**`allocation`**](#allocation-subcommand): Show actual resource utilization on nodes.


### `allocation` subcommand

Displays the resource allocation on nodes based on the sum of pod resource
requests. It shows each node's allocatable CPU and memory, the sum of CPU and
memory requests from pods running on them, and the percentage of allocatable
resources requested.

**Examples:**

1.  Show allocation for all nodes, sorted by CPU percentage (default sort):
    ```bash
    kubectl node-resource allocation
    ```

2.  Show allocation for nodes with the label `role=worker`, and also display host ports used by containers on these nodes:
    ```bash
    kubectl node-resource allocation "role=worker" --show-host-ports
    ```

3.  Show only the summary of allocation for nodes matching the label `pool=high-memory`, hiding the detailed table:
    ```bash
    kubectl node-resource allocation "pool=high-memory" --summary=only
    ```

4.  Show allocation for a specific node named `node1`, sorted by memory percentage, and include free (allocatable - requested) resources:
    ```bash
    kubectl node-resource allocation "kubernetes.io/hostname=node1" --sort-by=mem-percent --show-free
    ```

### `utilization` subcommand

Displays the actual resource utilization of nodes, similar to `kubectl top
node`. It shows each node's allocatable CPU and memory, the actual CPU and
memory currently used, and the percentage of allocatable resources utilized.
This command requires the Kubernetes metrics-server to be installed and running
in the cluster.

**Examples:**

1.  Show utilization for all nodes, sorted by CPU percentage (default sort):
    ```bash
    kubectl node-resource utilization
    ```

2.  Show utilization for nodes with the label `role=worker`:
    ```bash
    kubectl node-resource utilization "role=worker"
    ```

3.  Show utilization and include a column for free (allocatable - used) resources:
    ```bash
    kubectl node-resource utilization --show-free
    ```

4.  Show utilization for all nodes, sorted by memory percentage, and output in JSON format:
    ```bash
    kubectl node-resource utilization --sort-by=mem-percent --json
    ```

## License

This project is licensed under the Apache 2.0 License. See the
[LICENSE](LICENSE).
