// foundry-vm-acceptance is deliberately separate from the application server.
// It creates only disposable data and uses no real model accounts or secrets.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"foundry/internal/vmacceptance"
	"io"
	"os"
	"os/signal"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }
func run(args []string, out io.Writer) int {
	flags := flag.NewFlagSet("foundry-vm-acceptance", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var c vmacceptance.Config
	flags.BoolVar(&c.NativeVMAcceptance, "native-vm-acceptance", false, "explicitly authorize disposable native VM acceptance")
	flags.StringVar(&c.Bundle, "bundle", "", "absolute installed sandbox bundle")
	flags.StringVar(&c.DataDir, "data-dir", "", "absolute fresh private evidence directory")
	flags.StringVar(&c.Profile, "profile", "", "explicit closed v2 profile (omission preserves v1)")
	flags.StringVar(&c.Case, "case", "", "explicit closed v2 fixture case")
	parseErr := flags.Parse(args)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if parseErr != nil || flags.NArg() != 0 {
		c = vmacceptance.Config{}
	}
	result, err := vmacceptance.Run(ctx, c)
	if parseErr != nil || flags.NArg() != 0 {
		result.FailureCode = "invalid_arguments"
	}
	if e := json.NewEncoder(out).Encode(result); e != nil {
		return 1
	}
	if err != nil || !result.Passed {
		return 1
	}
	return 0
}
