# Quick Start

## Overview

This quick start guides you through installing and deploying Llumnix on a Kubernetes cluster.

## Prerequisites

- **Kubernetes cluster**: A running Kubernetes cluster with sufficient resources (including GPUs if you plan to run inference workloads).
- **LWS CRD installed (optional)**: The LeaderWorkerSet (LWS) custom resource definitions are required for wide EP (expert-parallel) workloads. See the official installation guide at `https://lws.sigs.k8s.io/docs/installation/`.
- **Sufficient permissions**: You must have permissions to install or update CRDs and cluster roles during infrastructure setup; after prerequisites are in place, namespace editor permissions are typically sufficient for deploying model servers.
- **kubectl configured locally**: `kubectl` (Kubernetes CLI) installed on your local machine with a kubeconfig that allows access to the target Kubernetes cluster. For installation instructions, see the [Kubernetes tools documentation](https://kubernetes.io/docs/tasks/tools/).

---

## Deployment

All deployment scripts and yamls are located in the `deploy/` directory. Choose the kustomize directory that matches your desired deployment mode before running any script.

Available kustomize directories:

| Directory | Description |
|---|---|
| `normal/lite-mode-scheduling/load-balance` | Normal mode, lite scheduling |
| `normal/full-mode-scheduling/load-balance` | Normal mode, full scheduling |
| `pd/full-mode-scheduling/load-balance` | Prefill-decode separated mode, full scheduling |
| `pd-kvs/full-mode-scheduling/load-balance` | Prefill-decode separated mode, full scheduling, with cache-aware scheduling based on prefix cache |

### Deploy

Run `group_deploy.sh` for the first deployment. It creates a Kubernetes namespace named `<group-name>` and applies the kustomize configuration. Each group is isolated in its own namespace.

Then deploy:

```bash
cd deploy/
./group_deploy.sh <group-name> <kustomize-dir>

# Examples:
./group_update.sh llumnix normal/full-mode-scheduling/load-balance
```

### Update

Use `group_update.sh` to apply configuration changes to an existing deployment. The namespace must already exist.

```bash
cd deploy/
./group_update.sh <group-name> <kustomize-dir>

# Example:
./group_update.sh llumnix normal/full-mode-scheduling/load-balance
```

### Cleanup

Use `group_delete.sh` to remove a deployment group and its namespace. The script lists all resources before deletion and asks for confirmation.

```bash
cd deploy/
./group_delete.sh <group-name>

# Example:
./group_delete.sh llumnix
```

## Verifying

After deployment, check that all pods and services are running:

```bash
kubectl get pods -o wide -n <group-name>
kubectl get service -o wide -n <group-name>
```

All pods should show a `Running` status. If any pod is in `Pending` or `CrashLoopBackOff`, inspect the logs:

```bash
kubectl logs -f <pod-name> -n <group-name>
```


## Benchmarking
