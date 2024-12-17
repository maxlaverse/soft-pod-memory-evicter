# soft-pod-memory-evicter

![Tests](https://github.com/maxlaverse/soft-pod-memory-evicter/actions/workflows/tests.yml/badge.svg?branch=main)
![Go Version](https://img.shields.io/github/go-mod/go-version/maxlaverse/soft-pod-memory-evicter)
![Releases](https://img.shields.io/github/v/release/maxlaverse/soft-pod-memory-evicter?include_prereleases)

A Kubernetes Controller that evicts Pods when they're reaching their memory limit, giving them a chance to properly shutdown.

## Supported Versions

The controller has been tested and built with the following components:
* Kubernetes >= 1.19
* Metric Server >= 0.5.0

## Installation

```bash
helm repo add maxlaverse https://maxlaverse.github.io/helm-charts/
helm repo update
helm install soft-pod-memory-evicter maxlaverse/soft-pod-memory-evicter
```

## Usage

```
NAME:
   soft-pod-memory-evicter - Gracefully evict Pods before they get OOM killed

USAGE:
   soft-pod-memory-evicter [global options] command [command options]

COMMANDS:
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --dry-run                            Output additional debug lines (default: false)
   --memory-usage-check-interval value  Interval at which the Pod metrics are checked (default: 3m0s)
   --memory-usage-threshold value       Memory usage eviction threshold (0-100) (default: 95)
   --loglevel value, -v value           Log Level (default: 0)
   --help, -h                           show help
```

## License

Distributed under the Apache License. See [LICENSE](./LICENSE) for more information.
