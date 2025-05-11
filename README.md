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

- [**`utilization`**](#utilization-subcommand): Show actual resource utilization
  on nodes.
- [**`allocation`**](#allocation-subcommand): Show pod resource allocations on
  nodes.

### `allocation` subcommand

Displays the resource allocation on nodes based on the sum of pod resource
requests. It shows each node's allocatable CPU and memory, the sum of CPU and
memory requests from pods running on them, and the percentage of allocatable
resources requested.

**Examples:**

<table>
  <thead>
    <tr>
      <th>Usage</th>
      <th>Command</th>
    </tr>
  </thead>
  <tbody>
    <tr>
      <td>Show allocation for all nodes, sorted by CPU percentage (default sort).</td>
      <td><pre>kubectl node-resource allocation</pre></td>
    </tr>
    <tr>
      <td>Show allocation for nodes with the label <code>role=worker</code>, and also display host ports used by containers on these nodes.</td>
      <td><pre>kubectl node-resource allocation "role=worker" \
	--show-host-ports</pre></td>
    </tr>
    <tr>
      <td>Show only the summary of allocation for nodes matching the label <code>pool=high-memory</code>, hiding the detailed table.</td>
      <td><pre>kubectl node-resource allocation "pool=high-memory" \
	--summary=only</pre></td>
    </tr>
    <tr>
      <td>Show allocation for a specific node named <code>node1</code>, sorted by memory percentage, and include free (allocatable - requested) resources.</td>
      <td><pre>kubectl node-resource allocation "kubernetes.io/hostname=node1" \
	--sort-by=mem-percent \
	--show-free</pre></td>
    </tr>
  </tbody>
</table>

### `utilization` subcommand

Displays the actual resource utilization of nodes, similar to `kubectl top
node`. It shows each node's allocatable CPU and memory, the actual CPU and
memory currently used, and the percentage of allocatable resources utilized.
This command requires the Kubernetes metrics-server to be installed and running
in the cluster.

**Examples:**

<table>
  <thead>
    <tr>
      <th>Usage</th>
      <th>Command</th>
    </tr>
  </thead>
  <tbody>
    <tr>
      <td>Show utilization for all nodes, sorted by CPU percentage (default sort).</td>
      <td><pre>kubectl node-resource utilization</pre></td>
    </tr>
    <tr>
      <td>Show utilization for nodes with the label <code>role=worker</code>.</td>
      <td><pre>kubectl node-resource utilization "role=worker"</pre></td>
    </tr>
    <tr>
      <td>Show utilization and include a column for free (allocatable - used) resources.</td>
      <td><pre>kubectl node-resource utilization \
	--show-free</pre></td>
    </tr>
    <tr>
      <td>Show utilization for all nodes, sorted by memory percentage, and output in JSON format.</td>
      <td><pre>kubectl node-resource utilization \
	--sort-by=mem-percent \
	--json</pre></td>
    </tr>
  </tbody>
</table>

## License

This project is licensed under the Apache 2.0 License. See the
[LICENSE](LICENSE).
