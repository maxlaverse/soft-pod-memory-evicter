package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/maxlaverse/soft-pod-memory-evicter/pkg"
	"github.com/urfave/cli/v2"
	"k8s.io/klog/v2"
)

func main() {
	opts := pkg.Options{
		DryRun:                   false,
		EvictionPause:            time.Duration(5) * time.Minute,
		MemoryUsageCheckInterval: time.Duration(3) * time.Minute,
		MemoryUsageThreshold:     95,
	}

	app := &cli.App{
		Name:  "soft-pod-memory-evicter",
		Usage: "Gracefully evict Pods before they get OOM killed",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "dry-run",
				Usage:       "Output additional debug lines",
				Value:       opts.DryRun,
				Destination: &opts.DryRun,
			}, &cli.DurationFlag{
				Name:        "eviction-pause",
				Usage:       "Pause duration between evictions",
				Value:       opts.EvictionPause,
				Destination: &opts.EvictionPause,
			}, &cli.DurationFlag{
				Name:        "memory-usage-check-interval",
				Usage:       "Interval at which the Pod metrics are checked",
				Value:       opts.MemoryUsageCheckInterval,
				Destination: &opts.MemoryUsageCheckInterval,
			}, &cli.IntFlag{
				Name:        "memory-usage-threshold",
				Usage:       "Memory usage eviction threshold (0-100)",
				Value:       opts.MemoryUsageThreshold,
				Destination: &opts.MemoryUsageThreshold,
			}, &cli.IntFlag{
				// We don't do anything with the verbose flag in the 'urfave/cli' module, but we
				// have to declare it nevertheless if we don't want to be thrown an "unknown argument" error.
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "verbose level",
				Value:   0,
			},
		},
		Action: func(c *cli.Context) error {
			klog.InitFlags(nil)
			defer klog.Flush()

			flag.Parse()

			ctx, cancel := signal.NotifyContext(c.Context, os.Interrupt)
			defer cancel()

			controller := pkg.NewController(opts)
			return controller.Run(ctx)
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
