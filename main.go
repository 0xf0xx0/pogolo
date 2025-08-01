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

	"github.com/0xf0xx0/oigiki"
	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/rpcclient"
	"github.com/urfave/cli/v3"
)

// state
var (
	conf            config.Config
	clients         map[string]StratumClient
	currTemplate    *JobTemplate
	submissionChan  chan *btcutil.Block
	serverStartTime time.Time
	// do we want to track found blocks? it won't be persisted...
)

func main() {
	app := &cli.Command{
		Name:                   "pogolo",
		Version:                "0.0.4",
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
			log("running on {yellow}%s", conf.Backend.ChainParams.Name)

			/// start
			return startup()
		},
	}
	if err := app.Run(context.Background(), os.Args); err != nil {
		logError("%s", err)
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
		log("conns start")
		for {
			select {
			case <-shutdown:
				{
					log("conns shutdown")
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
	log("\n{yellow}stopping")
	close(shutdown)
	wg.Wait()
	return nil
}

// handles individual conns
func clientHandler(conn net.Conn) {
	defer conn.Close()
	client := CreateClient(conn, submissionChan)
	channel := client.MsgChannel()
	/// remove ourself from the client map on disconnect
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
				log("new client \"{blue}%s{cyan}\" {white}%s", client.ID, client.Addr())
				/// i dont think the order matters, but lets send the current template
				/// before adding to the client map, just in case notifyClients gets
				/// called in between (and rapid-fires jobs)
				client.Channel() <- currTemplate
				clients[client.ID.String()] = client
			}
		case "done":
			{
				log("client disconnect \"{blue}%s{cyan}\" {white}%s", client.ID, client.Addr())
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
		logError("%s", err)
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
				logError("error from backend while submitting block: %s", err)
				continue
			}
			fmt.Println(
				oigiki.ProcessTags(
					"{bold}{green}===== BLOCK FOUND ===== BLOCK FOUND ===== BLOCK FOUND =====\nhash: %s",
					block.Hash(),
				),
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
			time.Sleep(time.Millisecond * time.Duration(conf.Backend.PollInterval))
		}
		for {
			count, err := backend.GetBlockCount()
			if err != nil {
				logError("%s", err)
			}
			/// we're mining on this height
			if count == currTemplate.Height {
				fmt.Sprintln(oigiki.ProcessTags("new bl00k in chain! {green}%d", count))
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
			/// TODO: gracefully handle backend errors
			logError("error fetching template: %s", err)
			time.Sleep(time.Millisecond * time.Duration(conf.Backend.PollInterval))
			continue
		}
		currTemplate = CreateJobTemplate(template)
		log("===<new template {blue}%s{cyan} with {green}%d{cyan} txns>===", currTemplate.ID, len(template.Transactions))
		/// this gets shipped to each StratumClient to become a full MiningJob
		go notifyClients(currTemplate) /// this might take a while
		select {
		case <-time.After(time.Second * time.Duration(conf.Pogolo.JobInterval)):
			{
			}
		/// shortcircuit
		case <-triggerGBT:
			{
			}
		}
	}
}

// listens on one ip
func listenerRoutine(shutdown chan struct{}, conns chan net.Conn, listener net.Listener) {
	defer listener.Close()
	log("listening on {white}%s", listener.Addr())
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-shutdown:
				{
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
		client.Channel() <- j
	}
}

func log(s string, a ...any) {
	fmt.Println(oigiki.ProcessTags(oigiki.TagString(fmt.Sprintf(s, a...), "cyan")))
}
func logError(s string, a ...any) {
	println(oigiki.ProcessTags(oigiki.TagString(fmt.Sprintf(s, a...), "red")))
}
