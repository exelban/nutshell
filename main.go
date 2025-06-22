package main

import (
	"context"
	"embed"
	"fmt"
	"github.com/jessevdk/go-flags"
	"github.com/pkgz/logg"
	"log"
	"nutshell/api"
	"nutshell/pkg"
	"nutshell/pkg/nut"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type arguments struct {
	UPSD struct {
		Host     string `long:"host" env:"HOST" description:"NUT server host"`
		Port     string `long:"port" env:"PORT" default:"3493" description:"NUT server port"`
		Username string `long:"username" env:"USERNAME" default:"upsmon" description:"NUT server username"`
		Password string `long:"password" env:"PASSWORD" default:"upsmon" description:"NUT server password"`
	} `group:"upsd" namespace:"upsd" env-namespace:"UPSD"`

	PoolInterval time.Duration `long:"pool-interval" env:"POOL_INTERVAL" default:"10s" description:"pool interval for NUT servers"`

	Addr string `long:"addr" env:"ADDR" default:"" description:"application address, empty for all interfaces"`
	Port int    `long:"port" env:"PORT" default:"8833" description:"application port"`

	Debug bool `long:"debug" env:"DEBUG" description:"debug mode"`
}

type app struct {
	srv *api.Server
	api *api.Rest

	args arguments
}

//go:embed template/*
var fs embed.FS
var version = "dev"

func main() {
	fmt.Println(version)

	var args arguments
	p := flags.NewParser(&args, flags.Default)
	if _, err := p.Parse(); err != nil {
		fmt.Printf("error parse args: %v", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		log.Print("[INFO] interrupt signal")
		cancel()
	}()

	logg.NewGlobal(os.Stdout)
	if args.Debug {
		logg.DebugMode()
	}

	app, err := create(ctx, args)
	if err != nil {
		log.Printf("[ERROR] create app: %v", err)
		os.Exit(1)
	}

	if err := app.run(ctx); err != nil {
		log.Printf("[ERROR] run app: %v", err)
		os.Exit(1)
	}
}

func create(ctx context.Context, args arguments) (*app, error) {
	if len(args.UPSD.Host) == 0 {
		return nil, fmt.Errorf("no NUT server configuration provided")
	}
	hosts := strings.Split(args.UPSD.Host, ",")
	ports := strings.Split(args.UPSD.Port, ",")
	usernames := strings.Split(args.UPSD.Username, ",")
	passwords := strings.Split(args.UPSD.Password, ",")

	clients := []*nut.Client{}
	for i, host := range hosts {
		port := "3493"
		username := "upsmon"
		password := "upsmon"

		if i < len(ports) {
			port = strings.TrimSpace(ports[i])
		}
		if i < len(usernames) {
			username = strings.TrimSpace(usernames[i])
		}
		if i < len(passwords) {
			password = strings.TrimSpace(passwords[i])
		}

		client, err := nut.New(ctx, host, port, username, password, args.PoolInterval)
		if err != nil {
			log.Printf("[ERROR] create client %s:%s: %v", host, port, err)
			continue
		}

		log.Printf("[DEBUG] connected to NUT %s:%s (VER=%s, NETVER=%s)", host, port, client.Version, client.ProtocolVersion)
		clients = append(clients, client)
	}

	return &app{
		srv: &api.Server{
			Port:    args.Port,
			Address: args.Addr,
		},
		api: &api.Rest{
			Template: &pkg.Template{
				FS:    fs,
				Debug: args.Debug,
			},
			Clients: clients,
		},

		args: args,
	}, nil
}

func (a *app) run(ctx context.Context) error {
	if err := a.api.Template.Run(ctx); err != nil {
		log.Printf("[ERROR] generate templates: %v", err)
	}

	go func() {
		if err := a.srv.Run(a.api.Router()); err != nil {
			log.Printf("[ERROR] run rest server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Print("[DEBUG] terminating...")

	if err := a.srv.Shutdown(); err != nil {
		log.Printf("[ERROR] rest shutdown %v", err)
	}

	for _, client := range a.api.Clients {
		if err := client.Disconnect(); err != nil {
			return fmt.Errorf("disconnect NUT client: %w", err)
		}
	}

	log.Print("[INFO] terminated")
	return nil
}
