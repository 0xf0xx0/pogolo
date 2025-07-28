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
	"github.com/urfave/cli/v3"
)

// state
var (
	conf           config.Config
	clients        map[string]StratumClient
	currTemplate   *JobTemplate
	submissionChan chan *btcutil.Block
)

func main() {
	app := &cli.Command{
		Name:                   "pogolo",
		Version:                "0.0.1",
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

			/// start
			return startup()
		},
	}
	if err := app.Run(context.Background(), os.Args); err != nil {
		println(fmt.Sprintf("%s", err))
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
			/// FIXME: ew, there has to be a better way
			addr := strings.Split(addr.String(), "/")[0]
			if strings.Contains(addr, ":") {
				addr = "["+addr+"]"
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

	/// connections
	go func() {
		defer wg.Done()
		wg.Add(1)
		fmt.Println("conns start")
		for {
			select {
			case <-shutdown:
				{
					fmt.Println("conns shutdown")
					return
				}
			case conn := <-conns:
				{
					go clientHandler(conn)
				}
			}
		}
	}()

	go backendRoutine()

	// wait for exit
	<-sigs
	fmt.Println("\nstopping")
	close(shutdown)
	wg.Wait()
	return nil
}

// handles incoming conns
func clientHandler(conn net.Conn) {
	defer conn.Close()
	client := CreateClient(conn, submissionChan)
	channel := client.Channel()
	/// remove ourself from the client map
	defer func() {
		delete(clients, client.ID.String())
	}()
	go client.Run(false)
	for {
		switch msg := (<-channel).(type) {
		case string:
			{
				switch msg {
				case "ready":
					{
						fmt.Printf("new client %q (%s)\n", client.ID, client.Addr())
						/// i dont think the order matters, but lets send the current template
						/// before adding to the client map, just in case notifyClients gets
						/// called in between (and rapid-fires jobs)
						client.Channel() <- currTemplate
						clients[client.ID.String()] = client
					}
				case "done":
					{
						fmt.Printf("client disconnect %q (%s)\n", client.ID, client.Addr())
						return
					}
				}
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
				continue
			}
			fmt.Println("===== BLOCK FOUND ===== BLOCK FOUND ===== BLOCK FOUND =====")
			fmt.Printf("hash: %s\n", block.Hash().String())
			/// TODO: trigger new job template?
		}
	}()
	/// poll getblockcount
	go func() {
		for {
			count, err := backend.GetBlockCount()
			if err != nil {
				fmt.Printf("%s\n", err)
			}
			/// we're mining on this height
			if count == currTemplate.Height {
				println("someone else mined a block:", count)
				triggerGBT <- true
			}
			time.Sleep(time.Millisecond * 500)
		}
	}()

	/// maingbt loop
	for {
		template, err := backend.GetBlockTemplate(&btcjson.TemplateRequest{
			Rules:        []string{"segwit"}, /// required by gbt
			Capabilities: []string{"proposal", "coinbasevalue", "longpoll"},
			Mode:         "template",
		})
		if err != nil {
			panic(err)
		}
		fmt.Printf("new template with %d txns\n", len(template.Transactions))
		/// this gets shipped to each StratumClient to become a full MiningJob
		currTemplate = CreateJobTemplate(template)
		go notifyClients(currTemplate) /// this might take a while
		/// TODO: 30s?
		select {
		case <-time.After(time.Minute):
			{
			}
			case <-triggerGBT: {}
		}
		//time.Sleep(time.Minute)
	}
}

// listens on one ip
func listenerRoutine(shutdown chan struct{}, conns chan net.Conn, listener net.Listener) {
	defer listener.Close()
	fmt.Printf("listening on %s\n", listener.Addr())
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

func notifyClients(n *JobTemplate) {
	for _, client := range clients {
		go func() {
			/// TODO: remove? move up?
			// defer func() {
			// 	if r := recover(); r != nil {
			// 		fmt.Println("recovered from a panic in notifyClients:", r)
			// 	}
			// }()
			client.Channel() <- n
		}()
	}
}
