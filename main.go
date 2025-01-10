package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
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
		ChannelQueueSize:         100,
		IgnoredNamespaces:        *cli.NewStringSlice(),
	}

	app := &cli.App{
		Name:  "soft-pod-memory-evicter",
		Usage: "Gracefully evict Pods before they get OOM killed",
		Before: func(c *cli.Context) error {
			fs := flag.NewFlagSet("", flag.PanicOnError)
			klog.InitFlags(fs)
			return fs.Set("v", strconv.Itoa(c.Int("loglevel")))
		},
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
				Name:        "channel-queue-size",
				Usage:       "Size of the queue for pod eviction",
				Value:       opts.ChannelQueueSize,
				Destination: &opts.ChannelQueueSize,
			}, &cli.StringSliceFlag{
				Name:        "ignore-namespace",
				Usage:       "Do not evict Pods from this namespace. Can be used multiple times",
				Value:       &opts.IgnoredNamespaces,
				Destination: &opts.IgnoredNamespaces,
			},
			&cli.IntFlag{
				Name:    "loglevel",
				Aliases: []string{"v"},
				Usage:   "Log Level",
				Value:   0,
			},
		},
		Action: func(c *cli.Context) error {
			defer klog.Flush()

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
