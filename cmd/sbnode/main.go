package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sbpanel/internal/node"
	"sbpanel/internal/updater"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  sbnode enroll --master host:port --token T --ca-sha256 FP [--dir D]
  sbnode run [--dir D]
  sbnode status [--dir D]
  sbnode version
  sbnode upgrade [--version vX.Y.Z] [--rollback] [--mirror URL] [--dir D]
  sbnode upgrade-helper --dir D   (internal: run by the panel's upgrade, outside the service)`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	if os.Args[1] == "upgrade" {
		cliUpgrade(os.Args[2:])
		return
	}
	if os.Args[1] == "upgrade-helper" {
		upgradeHelper(os.Args[2:])
		return
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	dir := fs.String("dir", envOr("SBN_DIR", "/var/lib/sbnode"), "state directory")
	masterAddr := fs.String("master", "", "master gRPC address host:port")
	token := fs.String("token", "", "one-time enrollment token")
	ca := fs.String("ca-sha256", "", "master CA fingerprint")
	fs.Parse(os.Args[2:])
	switch os.Args[1] {
	case "enroll":
		if *masterAddr == "" || *token == "" || *ca == "" {
			usage()
		}
		if err := node.Enroll(*dir, *masterAddr, *token, *ca); err != nil {
			log.Fatal(err)
		}
		fmt.Println("enrolled; start with: sbnode run --dir", *dir)
	case "run":
		a, err := node.NewAgent(node.Config{Dir: *dir})
		if err != nil {
			log.Fatal(err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := a.Run(ctx); err != nil {
			log.Fatal(err)
		}
	case "status":
		s, err := node.Status(*dir)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(s)
	case "version":
		fmt.Printf("sbnode %s (sing-box %s)\n", node.Version, node.SingboxVersion())
	default:
		usage()
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// upgradeHelper swaps in a staged binary, restarts the service and rolls back if it doesn't
// come up (internal/updater). It runs as its own transient unit, outside the sandbox.
func upgradeHelper(args []string) {
	fs := flag.NewFlagSet("upgrade-helper", flag.ExitOnError)
	dir := fs.String("dir", "", "upgrade directory holding plan.json")
	fs.Parse(args)
	if *dir == "" {
		usage()
	}
	if err := updater.Helper(*dir); err != nil {
		log.Fatal(err)
	}
}

// sbnode upgrade: upgrade the installed service from a shell (downloads from GitHub; the panel's
// node upgrade sends the binary itself), with health check and automatic rollback.
func cliUpgrade(args []string) {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	dir := fs.String("dir", envOr("SBN_DIR", "/var/lib/sbnode"), "state directory")
	version := fs.String("version", "", "release tag (default: the latest)")
	rollback := fs.Bool("rollback", false, "put the previous binary back")
	mirror := fs.String("mirror", "", "prefix for GitHub downloads, e.g. https://ghproxy.example/")
	fs.Parse(args)
	say := func(f string, a ...any) { fmt.Printf("==> "+f+"\n", a...) }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	r, err := updater.CLI{Binary: "sbnode", Dir: *dir, Version: *version, Rollback: *rollback, Mirror: *mirror, Timeout: 120}.Run(ctx, say)
	if err != nil {
		log.Fatal(err)
	}
	say("%s: %s -> %s", r.State, r.From, r.To)
	if r.Error != "" {
		fmt.Println(r.Error)
	}
	if r.State != updater.StateUpgraded {
		os.Exit(1)
	}
}
