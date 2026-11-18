package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"sbpanel/internal/master"
	"sbpanel/internal/updater"
	"sbpanel/web"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "import-3xui" {
		import3xui(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println("sbmaster", master.Version)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "upgrade-helper" {
		// swaps in a staged binary, restarts sbmaster and rolls back if it doesn't come up
		fs := flag.NewFlagSet("upgrade-helper", flag.ExitOnError)
		dir := fs.String("dir", "", "upgrade directory holding plan.json")
		fs.Parse(os.Args[2:])
		if *dir == "" {
			log.Fatal("usage: sbmaster upgrade-helper --dir DIR")
		}
		if err := updater.Helper(*dir); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "upgrade" {
		cliUpgrade("sbmaster", os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "reset-admin" {
		resetAdmin(os.Args[2:])
		return
	}
	var cfg master.Config
	flag.StringVar(&cfg.DataDir, "data", env("SBM_DATA", "/var/lib/sbmaster"), "data directory")
	flag.StringVar(&cfg.WebListen, "web", env("SBM_WEB", "127.0.0.1:8080"), "web/API listen address")
	flag.StringVar(&cfg.GRPCListen, "grpc", env("SBM_GRPC", ":9443"), "node gRPC listen address")
	flag.StringVar(&cfg.PublicGRPCAddr, "public-grpc", env("SBM_PUBLIC_GRPC", ""), "host:port nodes dial (shown in enroll command)")
	flag.StringVar(&cfg.TZ, "tz", env("SBM_TZ", "Asia/Shanghai"), "time zone for day/month boundaries")
	flag.Parse()
	cfg.AdminPassword = os.Getenv("SBM_ADMIN_PASSWORD")
	cfg.Web = web.FS()
	m, err := master.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := m.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

// sbmaster import-3xui --data DIR --node ID (--db x-ui.db | --json export.json) [--dry-run]
func import3xui(args []string) {
	fs := flag.NewFlagSet("import-3xui", flag.ExitOnError)
	data := fs.String("data", env("SBM_DATA", "/var/lib/sbmaster"), "sbmaster data directory")
	node := fs.String("node", "", "target node id (see the Nodes page)")
	dbPath := fs.String("db", "", "3x-ui x-ui.db (SQLite)")
	jsonPath := fs.String("json", "", "3x-ui API export (GET /panel/api/inbounds/list)")
	dry := fs.Bool("dry-run", false, "only report what would be imported")
	usersOnly := fs.Bool("users-only", false, "import only users (into the default groups); no inbounds, no --node needed")
	fs.Parse(args)
	if (*node == "" && !*usersOnly) || (*dbPath == "") == (*jsonPath == "") {
		log.Fatal("usage: sbmaster import-3xui (--node ID | --users-only) (--db x-ui.db | --json export.json) [--dry-run]")
	}
	var ins []master.XUIInbound
	var err error
	if *dbPath != "" {
		ins, err = master.LoadXUIDB(*dbPath)
	} else {
		var b []byte
		if b, err = os.ReadFile(*jsonPath); err == nil {
			ins, err = master.ParseXUIExport(b)
		}
	}
	if err != nil {
		log.Fatal(err)
	}
	m, err := master.New(master.Config{DataDir: *data})
	if err != nil {
		log.Fatal(err)
	}
	var rep *master.ImportReport
	if *usersOnly {
		rep, err = m.Import3XUIUsers(ins, *dry)
	} else {
		rep, err = m.Import3XUI(*node, ins, *dry)
	}
	if err != nil {
		log.Fatal(err)
	}
	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(out))
}

// sbmaster reset-admin --data DIR [--user NAME] [--rename OLD] --password NEW|- (also clears 2FA)
// --password - reads the password from stdin, keeping it off the command line.
func resetAdmin(args []string) {
	fs := flag.NewFlagSet("reset-admin", flag.ExitOnError)
	data := fs.String("data", env("SBM_DATA", "/var/lib/sbmaster"), "sbmaster data directory")
	user := fs.String("user", "admin", "admin username")
	pw := fs.String("password", "", "new password (min 8 chars); - reads it from stdin")
	rename := fs.String("rename", "", "rename this existing admin to --user first")
	fs.Parse(args)
	if *pw == "-" {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
		if err != nil {
			log.Fatal(err)
		}
		*pw = strings.TrimRight(string(b), "\r\n")
	}
	if len(*pw) < 8 {
		log.Fatal("--password must be at least 8 characters")
	}
	m, err := master.New(master.Config{DataDir: *data})
	if err != nil {
		log.Fatal(err)
	}
	if *rename != "" && *rename != *user {
		if err := m.RenameAdmin(*rename, *user); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("renamed admin %s -> %s\n", *rename, *user)
	}
	if err := m.ResetAdmin(*user, *pw); err != nil {
		log.Fatal(err)
	}
	fmt.Println("password reset and 2FA cleared for", *user)
}

// sbmaster upgrade [--version vX.Y.Z] [--rollback] [--mirror URL]: upgrade the installed service
// from a shell, with the same verification, health check and automatic rollback as the panel.
func cliUpgrade(bin string, args []string) {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	data := fs.String("data", env("SBM_DATA", "/var/lib/sbmaster"), "data directory")
	version := fs.String("version", "", "release tag (default: the latest)")
	rollback := fs.Bool("rollback", false, "put the previous binary back")
	mirror := fs.String("mirror", "", "prefix for GitHub downloads, e.g. https://ghproxy.example/")
	fs.Parse(args)
	say := func(f string, a ...any) { fmt.Printf("==> "+f+"\n", a...) }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	r, err := updater.CLI{Binary: bin, Dir: *data, Version: *version, Rollback: *rollback, Mirror: *mirror, Timeout: 90}.Run(ctx, say)
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
