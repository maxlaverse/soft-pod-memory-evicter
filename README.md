# pod-soft-memory-evicter

![Tests](https://github.com/maxlaverse/pod-soft-memory-evicter/actions/workflows/tests.yml/badge.svg?branch=main)
![Go Version](https://img.shields.io/github/go-mod/go-version/maxlaverse/pod-soft-memory-evicter)
![Releases](https://img.shields.io/github/v/release/maxlaverse/pod-soft-memory-evicter?include_prereleases)

A Kubernetes Controller that evicts Pods when they're reaching their memory limit, giving them a chance to properly shutdown.

## Supported Versions

The controller has been tested and built with the following components:
* Kubernetes >= 1.19
* Metric Server >= 0.5.0

## Installation

```bash
helm repo add maxlaverse https://maxlaverse.github.io/helm-charts/
helm repo update
helm install pod-soft-memory-evicter maxlaverse/pod-soft-memory-evicter
```

## License

Distributed under the Apache License. See [LICENSE](./LICENSE) for more information.