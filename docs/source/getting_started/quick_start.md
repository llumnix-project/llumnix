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
| `neutral/lite-mode-scheduling/load-balance` | Neutral mode, lite scheduling |
| `neutral/full-mode-scheduling/load-balance` | Neutral mode, full scheduling |
| `pd/full-mode-scheduling/load-balance` | Prefill-decode separated mode, full scheduling |
| `pd-kvs/full-mode-scheduling/load-balance` | Prefill-decode separated mode, full scheduling, with cache-aware scheduling based on prefix cache |

### Deploy

Run `group_deploy.sh` for the first deployment. It creates a Kubernetes namespace named `<group-name>` and applies the kustomize configuration. Each group is isolated in its own namespace.

Then deploy:

```bash
cd deploy/
./group_deploy.sh <group-name> <kustomize-dir>

# Examples:
./group_deploy.sh llumnix neutral/full-mode-scheduling/load-balance
```

### Update

Use `group_update.sh` to apply configuration changes to an existing deployment. The namespace must already exist.

```bash
cd deploy/
./group_update.sh <group-name> <kustomize-dir>

# Example:
./group_update.sh llumnix neutral/full-mode-scheduling/load-balance

# Example with custom image repository / tags:
./group_update.sh llumnix neutral/full-mode-scheduling/load-balance \
  --repository my-registry.example.com/my-namespace \
  --gateway-tag 20260101-120000 \
  --scheduler-tag 20260101-130000 \
  --vllm-tag 20260101-140000 \
  --discovery-tag 20260101-150000 \
  --mooncake-vllm-tag mooncake-20260101-160000
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

# Example:
kubectl get pods -n llumnix
kubectl get service -n llumnix
```

```bash
kubectl get pods -n llumnix
NAME                         READY   STATUS    RESTARTS   AGE
gateway-6d9795fdf7-8qn6t     1/1     Running   0          3m40s
llumnix-benchmark-fxfx9      1/1     Running   0          26m
neutral-0                    2/2     Running   0          3m39s
redis-766d49b5f-4zrbd        1/1     Running   0          20h
scheduler-5666794775-tfxcj   1/1     Running   0          3m40s

kubectl get service  -n llumnix
NAME                      TYPE        CLUSTER-IP     EXTERNAL-IP   PORT(S)                                 AGE
gateway                   ClusterIP   10.0.17.211    <none>        8089/TCP                                2d23h
mooncake-master-service   ClusterIP   10.0.210.133   <none>        50051/TCP,50052/TCP,9003/TCP,8000/TCP   170m
neutral                   ClusterIP   None           <none>        <none>                                  4m14s
redis                     ClusterIP   10.0.80.96     <none>        6379/TCP                                2d23h
scheduler                 ClusterIP   10.0.64.41     <none>        8088/TCP                                2d23h
```

All pods should show a `Running` status. If any pod is in `Pending` or `CrashLoopBackOff`, inspect the logs:

```bash
kubectl logs -f <pod-name> -n <group-name>

# Example:
kubectl logs -f neutral-0 -n llumnix
```


## Benchmarking

Use `benchmarks/run_benchmark.sh` to run a benchmark job against a deployed Llumnix gateway:

```bash
cd benchmarks/
./run_benchmark.sh -n llumnix
```

The script prints the `kubectl cp` command to retrieve results once the benchmark completes. For full usage and options, see [Benchmark Guide](source/getting_started/benchmark.md).
