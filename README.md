# Kubectl Parallel Scale Down Plugin

This is a kubectl plugin written in Go that scales down a list of Deployments and StatefulSets provided in a YAML file in parallel. It is designed to speed up the process of scaling down multiple resources before maintenance or upgrades.

## Prerequisites

- Go 1.25+ installed (for building)
- Access to a Kubernetes cluster (valid kubeconfig)

## Build

```bash
go mod tidy
go build -o kubectl-parallel_scale_down main.go
```

## Installation Install as a Kubectl Plugin

To use this tool effectively with `kubectl`, the binary must be named `kubectl-parallel_scale_down` and placed in your system's `$PATH`.

1.  **Build the binary**:
    ```bash
    go build -o kubectl-parallel_scale_down main.go
    ```

2.  **Install to PATH**:
    Move the binary to a directory included in your `$PATH` (e.g., `/usr/local/bin` or `$HOME/go/bin`).

    ```bash
    chmod +x kubectl-parallel_scale_down
    sudo mv kubectl-parallel_scale_down /usr/local/bin/
    ```

3.  **Verify Installation**:
    Check if `kubectl` recognizes the plugin.

    ```bash
    kubectl plugin list | grep parallel
    ```
    You should see `kubectl-parallel_scale_down` in the output.

## Usage

### 1. Create an Input Configuration

Create a YAML file (e.g., `input.yaml`) that defines the resources you want to scale.

**Example `input.yaml`:**

```yaml
deployments:
  - name: deploy-1
    namespace: ns1
    replicas: 0 # Target replicas. Defaults to 0 if omitted.
  - name: deploy-2
    namespace: ns2
    
statefulsets:
  - name: state-1
    namespace: ns3
    replicas: 0
```

### 2. Run the Command

Once installed as a plugin, you can invoke it like a native kubectl command. Note that the plugin name `parallel_scale_down` becomes `parallel-scale-down` when invoked (kubectl handles the hyphen/underscore conversion).

```bash
kubectl parallel-scale-down --file input.yaml
```

### Command Flags

- `--file`: (Required) Path to the input YAML file containing the list of deployments and statefulsets.
- `-h, --help`: Display help information.

## How it Works

1.  **Parallel Execution**: The plugin launches a separate goroutine for every resource listed in your input file.
2.  **Scale Action**: It sends a patch request to update specific `replicas` count (default 0).
3.  **Watch & Wait**: It uses the Kubernetes API to poll the resource status until `status.replicas` matches the target.
4.  **Error Aggregation**: If any resource fails (e.g., "Not Found", "Forbidden"), errors are collected.
5.  **Completion**: 
    - **Success**: A confirmation message is printed only when ALL resources have successfully consolidated to the target replica count.
    - **Failure**: A summary of all failed resources and their specific errors is printed at the end.

## Troubleshooting

- **"command not found"**: Ensure the binary is in your `$PATH` and is executable (`chmod +x`).
- **"resource not found"**: Check your `input.yaml` for typos in `name` or `namespace`. The tool will report exactly which resource was missing.
