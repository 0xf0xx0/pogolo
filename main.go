package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"pogolo/config"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/rpcclient"
	"github.com/fatih/color"
	"github.com/urfave/cli/v3"
)

// state
var (
	conf            config.Config
	clients         map[string]StratumClient
	currTemplate    *JobTemplate
	submissionChan  chan *btcutil.Block
	serverStartTime time.Time
)

func main() {
	app := &cli.Command{
		Name:                   "pogolo",
		Version:                "0.0.3",
		Usage:                  "local go pool",
		UsageText:              "pogolo [options]",
		UseShortOptionHandling: true,
		EnableShellCompletion:  true,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "conf",
				Usage: "config file `path`",
				Value: filepath.Join(config.ROOT, "pogolo.toml"),
			},
		},
		Action: func(_ context.Context, ctx *cli.Command) error {
			/// set defaults
			config.DeepCopyConfig(&conf, &config.DEFAULT_CONFIG)
			if passedConfig := ctx.String("conf"); passedConfig != "" {
				config.LoadConfig(passedConfig, &conf)
			}
			switch conf.Backend.Chain {
			case "mainnet":
				{
					conf.Backend.ChainParams = &chaincfg.MainNetParams
				}
			case "testnet":
				{
					/// TODO: replace with testnet4 next btcd update
					conf.Backend.ChainParams = &chaincfg.TestNet3Params
				}
			case "regtest":
				{
					conf.Backend.ChainParams = &chaincfg.RegressionNetParams
				}
			case "signet":
				{
					conf.Backend.ChainParams = &chaincfg.SigNetParams
				}
			default:
				{
					return cli.Exit("unknown backend chain", 3)
				}
			}
			color.Cyan("running on %s", color.YellowString(conf.Backend.ChainParams.Name))

			/// start
			return startup()
		},
	}
	if err := app.Run(context.Background(), os.Args); err != nil {
		println(color.RedString("%s", err))
	}
}

func startup() error {
	var wg sync.WaitGroup
	sigs := make(chan os.Signal, 1)
	shutdown := make(chan struct{})

	conns := make(chan net.Conn)
	clients = make(map[string]StratumClient, 5)
	submissionChan = make(chan *btcutil.Block)

	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	/// listener
	if conf.Pogolo.Interface != "" {
		inter, err := net.InterfaceByName(conf.Pogolo.Interface)
		if err != nil {
			return cli.Exit(err.Error(), 1)
		}
		addrs, err := inter.Addrs()
		if err != nil {
			return cli.Exit(err.Error(), 1)
		}
		for _, addr := range addrs {
			addr := strings.Split(addr.String(), "/")[0]
			if strings.Contains(addr, ":") {
				addr = "[" + addr + "]"
			}
			listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", addr, conf.Pogolo.Port))
			if err != nil {
				return cli.Exit(err.Error(), 1)
			}
			go listenerRoutine(shutdown, conns, listener)
		}
	} else {
		listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", conf.Pogolo.IP, conf.Pogolo.Port))
		if err != nil {
			return cli.Exit(err.Error(), 1)
		}
		go listenerRoutine(shutdown, conns, listener)
	}

	go backendRoutine()

	/// connections
	go func() {
		defer wg.Done()
		wg.Add(1)
		color.Cyan("conns start")
		for {
			select {
			case <-shutdown:
				{
					color.Cyan("conns shutdown")
					return
				}
			case conn := <-conns:
				{
					go clientHandler(conn)
				}
			}
		}
	}()

	serverStartTime = time.Now()
	// wait for exit
	<-sigs
	color.Yellow("\nstopping")
	close(shutdown)
	wg.Wait()
	return nil
}

// handles individual conns
func clientHandler(conn net.Conn) {
	defer conn.Close()
	client := CreateClient(conn, submissionChan)
	channel := client.MsgChannel()
	/// remove ourself from the client map
	defer func() {
		delete(clients, client.ID.String())
	}()
	go client.Run(false)
	for {
		msg, ok := <-channel
		if !ok {
			return
		}

		switch msg {
		case "ready":
			{
				color.Blue("new client \"%s\" %s\n", client.ID, color.WhiteString(client.Addr().String()))
				/// i dont think the order matters, but lets send the current template
				/// before adding to the client map, just in case notifyClients gets
				/// called in between (and rapid-fires jobs)
				client.Channel() <- currTemplate
				clients[client.ID.String()] = client
			}
		case "done":
			{
				color.Blue("client disconnect \"%s\" (%s)\n", client.ID, color.WhiteString(client.Addr().String()))
				return
			}
		}
	}
}

// handles templates and block submissions
func backendRoutine() {
	/// TODO: longpoll?
	/// TODO: do we need anything special for btcd/knots/etc?
	var backend *rpcclient.Client
	var err error
	triggerGBT := make(chan bool)
	if conf.Backend.Cookie != "" {
		backend, err = rpcclient.New(&rpcclient.ConnConfig{
			Host:                conf.Backend.Host,
			CookiePath:          conf.Backend.Cookie,
			DisableTLS:          true,
			DisableConnectOnNew: true,
			HTTPPostMode:        true,
		}, nil)
	} else if conf.Backend.Rpcauth != "" && strings.Contains(conf.Backend.Rpcauth, ":") {
		auth := strings.Split(conf.Backend.Rpcauth, ":")
		backend, err = rpcclient.New(&rpcclient.ConnConfig{
			Host:                conf.Backend.Host,
			User:                auth[0],
			Pass:                auth[1],
			DisableTLS:          true,
			DisableConnectOnNew: true,
			HTTPPostMode:        true,
		}, nil)
	} else {
		err = cli.Exit("neither valid rpc cookie nor valid auth string in config", 2)
	}
	if err != nil {
		fmt.Printf("%s\n", err)
		return
	}
	/// block submissions
	go func() {
		for {
			/// furst come furst serve
			block, ok := <-submissionChan
			if !ok {
				println("failed to receive block submission")
				continue
			}
			err := backend.SubmitBlock(block, nil)
			if err != nil {
				println(fmt.Sprintf("error from backend while submitting block: %s", err))
				println(block.Hash().String())
				continue
			}
			boldGreen := color.New(color.Bold, color.FgGreen).Sprint
			fmt.Println(
				boldGreen("===== BLOCK FOUND ===== BLOCK FOUND ===== BLOCK FOUND =====") +
					/// MAYBE: log worker?
					fmt.Sprintf("\nhash: %s", boldGreen(block.Hash().String())),
			)

			triggerGBT <- true
		}
	}()
	/// poll getblockcount
	go func() {
		/// needed to start after the gbt loop
		for {
			if currTemplate != nil {
				break
			}
			time.Sleep(time.Second)
		}
		for {
			count, err := backend.GetBlockCount()
			if err != nil {
				fmt.Printf("%s\n", err)
			}
			/// we're mining on this height
			if count == currTemplate.Height {
				color.Cyan("new bl00k in chain! %d", count)
				/// FIXME/MAYBE: skip when we mine a block?
				/// it triggers gbt before the winning block trigger completes sometimes
				triggerGBT <- true
			}
			time.Sleep(time.Millisecond * time.Duration(conf.Backend.PollInterval))
		}
	}()

	/// main gbt loop
	for {
		template, err := backend.GetBlockTemplate(&btcjson.TemplateRequest{
			Rules:        []string{"segwit"}, /// required by gbt
			Capabilities: []string{"proposal", "coinbasevalue", "longpoll"},
			Mode:         "template",
		})
		if err != nil {
			panic(err)
		}
		currTemplate = CreateJobTemplate(template)
		color.Cyan("===<new template %s with %d txns>===\n", currTemplate.ID, len(template.Transactions))
		/// this gets shipped to each StratumClient to become a full MiningJob
		go notifyClients(currTemplate) /// this might take a while
		select {
		case <-time.After(time.Second * time.Duration(conf.Pogolo.JobInterval)):
			{
			}
		case <-triggerGBT:
			{
			}
		}
	}
}

// listens on one ip
func listenerRoutine(shutdown chan struct{}, conns chan net.Conn, listener net.Listener) {
	defer listener.Close()
	color.Cyan("listening on %s\n", color.WhiteString(listener.Addr().String()))
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-shutdown:
				{
					//fmt.Println("srv shutdown")
					return
				}
			default:
				{
					println(err.Error())
					continue
				}
			}
		}
		conns <- conn
	}
}

// func apiServer() {}

func notifyClients(j *JobTemplate) {
	for _, client := range clients {
		/// TODO: remove? move up?
		// defer func() {
		// 	if r := recover(); r != nil {
		// 		fmt.Println("recovered from a panic in notifyClients:", r)
		// 	}
		// }()
		client.Channel() <- j
		println("notified")
	}
}
