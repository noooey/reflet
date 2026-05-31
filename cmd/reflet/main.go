package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/kyuyeonpark/reflet/internal/proxy"
	"github.com/kyuyeonpark/reflet/internal/runner"
	"github.com/kyuyeonpark/reflet/internal/storage"
)

const defaultAddr = "127.0.0.1:17381"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("missing command")
	}

	store, err := storage.NewStore("reflet")
	if err != nil {
		return err
	}

	switch args[0] {
	case "set":
		return runSet(store, args[1:])
	case "list":
		return runList(store)
	case "remove":
		return runRemove(store, args[1:])
	case "proxy":
		return runProxy(store, args[1:])
	case "run":
		return runChild(store, args[1:])
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runSet(store *storage.Store, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: reflet set <name>")
	}
	value, err := storage.PromptSecret(args[0])
	if err != nil {
		return err
	}
	if err := store.Set(args[0], value); err != nil {
		return err
	}
	fmt.Printf("stored %s as ref://%s\n", args[0], args[0])
	return nil
}

func runList(store *storage.Store) error {
	keys, err := store.List()
	if err != nil {
		return err
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Println(key)
	}
	return nil
}

func runRemove(store *storage.Store, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: reflet remove <name>")
	}
	if err := store.Remove(args[0]); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", args[0])
	return nil
}

func runProxy(store *storage.Store, args []string) error {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	addr := fs.String("addr", defaultAddr, "listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}

	p, err := proxy.New(*addr, store)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return p.Run(ctx)
}

func runChild(store *storage.Store, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	addr := fs.String("addr", defaultAddr, "proxy address")
	var envPairs runner.EnvPairs
	fs.Var(&envPairs, "e", "environment assignment in NAME=ref://secret format; repeatable")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cmdArgs := fs.Args()
	if len(cmdArgs) == 0 {
		return errors.New("usage: reflet run -e NAME=ref://secret -- <command> [args...]")
	}
	if len(envPairs) == 0 {
		return errors.New("at least one -e NAME=ref://secret assignment is required")
	}

	cfg, err := store.Config()
	if err != nil {
		return err
	}

	r := runner.New(*addr, cfg.CAPath)
	return r.Run(cmdArgs, envPairs)
}

func usage() {
	lines := []string{
		"Usage:",
		"  reflet set <name>",
		"  reflet list",
		"  reflet remove <name>",
		"  reflet proxy [-addr 127.0.0.1:17381]",
		"  reflet run -e NAME=ref://secret -- <command> [args...]",
		"",
		"Examples:",
		"  reflet set openai-api-key",
		"  reflet run -e OPENAI_API_KEY=ref://openai-api-key -- curl https://api.openai.com/v1/models",
	}
	fmt.Fprintln(os.Stderr, strings.Join(lines, "\n"))
}
