# soft-pod-memory-evicter

![Tests](https://github.com/maxlaverse/soft-pod-memory-evicter/actions/workflows/tests.yml/badge.svg?branch=main)
![Go Version](https://img.shields.io/github/go-mod/go-version/maxlaverse/soft-pod-memory-evicter)
![Releases](https://img.shields.io/github/v/release/maxlaverse/soft-pod-memory-evicter?include_prereleases)

A Kubernetes Controller that proactively evicts Pods approaching their memory limits, enabling graceful shutdowns before OOM (Out of Memory) termination occurs.

## Supported Versions

The controller has been tested and built with the following components:

- Kubernetes >= 1.32.1
- Metric Server >= 0.7.2

## Installation

```bash
helm repo add maxlaverse https://maxlaverse.github.io/helm-charts/
helm repo update
helm install soft-pod-memory-evicter maxlaverse/soft-pod-memory-evicter
```

The controller can be configured by setting Helm values under `extraArgs: []` with any of the options listed in the [Usage](#usage) section below. For a complete list of configurable Helm values, please refer to the [values.yaml][values] file.

## Usage

```
NAME:
   soft-pod-memory-evicter - Gracefully evict Pods before they get OOM killed

USAGE:
   soft-pod-memory-evicter [global options] command [command options]

COMMANDS:
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --dry-run                                              Output additional debug lines (default: false)
   --pod-selector value                                   Evict only Pods matching this label selector
   --eviction-pause value                                 Pause duration between evictions (default: 5m0s)
   --memory-usage-check-interval value                    Interval at which the Pod metrics are checked (default: 3m0s)
   --memory-usage-threshold value                         Memory usage eviction threshold (0-100) (default: 95)
   --channel-queue-size value                             Size of the queue for pod eviction (default: 100)
   --ignore-namespace value [ --ignore-namespace value ]  Do not evict Pods from this namespace. Can be used multiple times
   --loglevel value, -v value                             Log Level (default: 0)
   --help, -h                                             show help
```

## Pod-specific configurations

Pods can be configured individually using annotations to override the default behavior:

- `soft-pod-memory-evicter.laverse.net/memory-usage-threshold`: Specifies the memory usage threshold (0-100) at which the Pod should be evicted. This value overrides the global threshold set by the `--memory-usage-threshold` flag.

Example:

```yaml
apiVersion: v1
kind: Pod
metadata:
  annotations:
    soft-pod-memory-evicter.laverse.net/memory-usage-threshold: "80"
```

## License

Distributed under the Apache License. See [LICENSE](./LICENSE) for more information.

[values]: https://github.com/maxlaverse/helm-charts/blob/main/charts/soft-pod-memory-evicter/values.yaml
