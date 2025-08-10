package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"pogolo/config"
	"pogolo/constants"
	"runtime/pprof"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/0xf0xx0/oigiki"
	"github.com/0xf0xx0/stratum"
	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/rpcclient"
	"github.com/btcsuite/btcd/wire"
	"github.com/urfave/cli/v3"
)

// things
const (
	NAME    = "pogolo"
	VERSION = "0.0.7"
)

// state
var (
	backend           *rpcclient.Client
	activeChainParams *chaincfg.Params
	conf              config.Config
	clients           map[stratum.ID]*StratumClient // map of client ids to clients
	currTemplate      *JobTemplate
	submissionChan    chan BlockSubmission
	serverStartTime   time.Time
)

func main() {
	app := &cli.Command{
		Name:                   NAME,
		Version:                VERSION,
		Usage:                  "foss is freedom",
		UsageText:              "pogolo [options]",
		UseShortOptionHandling: true,
		EnableShellCompletion:  true,
		// ExitErrHandler: func(ctx context.Context, c *cli.Command, err error) {
		// },
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "conf",
				Usage: "config file `path`",
				Value: filepath.Join(config.ROOT, "pogolo.toml"),
			},
			&cli.StringFlag{
				Name:  "writedefaultconf",
				Usage: "write default config to `path`",
			},
			&cli.BoolFlag{
				Name:   "profile",
				Hidden: true,
			},
		},
		Action: func(_ context.Context, ctx *cli.Command) error {
			if ctx.Bool("profile") {
				log("{bold}=/=<profiling>=/=")
				profileFile, err := os.Create("cpu.prof")
				if err != nil {
					return err
				}
				memProfFile, err := os.Create("mem.prof")
				if err != nil {
					return err
				}
				pprof.StartCPUProfile(profileFile)
				defer pprof.WriteHeapProfile(memProfFile)
				defer pprof.StopCPUProfile()
			}
			if ctx.String("writedefaultconf") != "" {
				config.WriteDefaultConfig(ctx.String("writedefaultconf"))
				return nil
			}
			/// set defaults
			config.DeepCopyConfig(&conf, &config.DEFAULT_CONFIG)
			if passedConfig := ctx.String("conf"); passedConfig != "" && passedConfig != "none" {
				if err := config.LoadConfig(passedConfig, &conf); err != nil {
					return cli.Exit(fmt.Sprintf("error loading config: %s", err), constants.ERROR_CONFIG)
				}
			}

			/// init backend
			backendConnConf := &rpcclient.ConnConfig{
				Host:         conf.Backend.Host,
				DisableTLS:   true,
				HTTPPostMode: !conf.Backend.Websocket,
			}
			if conf.Backend.Websocket {
				backendConnConf.Endpoint = "ws"
			}
			if conf.Backend.Cookie != "" {
				backendConnConf.CookiePath = conf.Backend.Cookie
			} else if conf.Backend.Rpcauth != "" && strings.Contains(conf.Backend.Rpcauth, ":") {
				auth := strings.Split(conf.Backend.Rpcauth, ":")
				backendConnConf.User = auth[0]
				backendConnConf.Pass = auth[1]
			} else {
				return cli.Exit("neither valid rpc cookie path nor valid auth string in config", constants.ERROR_CONFIG)
			}
			var err error
			backend, err = rpcclient.New(backendConnConf, &rpcclient.NotificationHandlers{
				/// we do not care
				OnFilteredBlockConnected:    func(height int32, _ *wire.BlockHeader, _ []*btcutil.Tx) {},
				OnFilteredBlockDisconnected: func(height int32, header *wire.BlockHeader) {},
				/// only needed for the backend.NotifyBlocks() call later
			})
			if err != nil {
				return cli.Exit(fmt.Sprintf("failed to connect to backend: %s", err), constants.ERROR_BACKEND)
			}

			mininginfo, err := backend.GetBlockChainInfo()
			if err != nil {
				return cli.Exit(fmt.Sprintf("failed to get chain info: %s", err), constants.ERROR_BACKEND)
			}

			switch mininginfo.Chain {
			case "main":
				{
					activeChainParams = &chaincfg.MainNetParams
				}
			case "test":
				{
					activeChainParams = &chaincfg.TestNet3Params
				}
			case "testnet4":
				{
					activeChainParams = &chaincfg.TestNet4Params
				}
			case "regtest":
				{
					activeChainParams = &chaincfg.RegressionNetParams
				}
			case "signet":
				{
					activeChainParams = &chaincfg.SigNetParams
				}
			default:
				{
					return cli.Exit("unknown backend chain", constants.ERROR_BACKEND)
				}
			}

			/// start
			log("===<{bold}{blue}%s {bold}{green}v%s{bold}{blue} - %s{cyan}>===", ctx.Name, ctx.Version, ctx.Usage)
			log("mining on {yellow}%s", activeChainParams.Name)
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
	clients = make(map[stratum.ID]*StratumClient, 5)
	submissionChan = make(chan BlockSubmission)

	initAPI()

	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	/// listener
	if conf.Pogolo.Interface != "" {
		inter, err := net.InterfaceByName(conf.Pogolo.Interface)
		if err != nil {
			return cli.Exit(fmt.Sprintf("error binding to interface: %s", err), constants.ERROR_NET)
		}
		addrs, err := inter.Addrs()
		if err != nil {
			return cli.Exit(err.Error(), constants.ERROR_NET)
		}
		for _, addr := range addrs {
			addr := strings.Split(addr.String(), "/")[0]
			if strings.Contains(addr, ":") {
				addr = "[" + addr + "]"
			}
			listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", addr, conf.Pogolo.Port))
			if err != nil {
				return cli.Exit(fmt.Sprintf("error listening on ip %q: %s", addr, err), constants.ERROR_NET)
			}
			go listenerRoutine(shutdown, conns, listener, fmt.Sprintf("%s:%d", addr, conf.Pogolo.HTTPPort))
		}
	} else {
		listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", conf.Pogolo.IP, conf.Pogolo.Port))
		if err != nil {
			return cli.Exit(err.Error(), constants.ERROR_NET)
		}
		go listenerRoutine(shutdown, conns, listener, fmt.Sprintf("%s:%d", conf.Pogolo.IP, conf.Pogolo.HTTPPort))
	}

	go backendRoutine()

	/// connections
	go func() {
		defer wg.Done()
		wg.Add(1)
		for {
			select {
			case <-shutdown:
				{
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
	if conf.Backend.Websocket {
		backend.Shutdown()
		backend.WaitForShutdown()
	}
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
		delete(clients, client.ID)
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
				/// i dont think the order matters, but lets send the current template
				/// before adding to the client map, just in case notifyClients gets
				/// called in between (and rapid-fires jobs)
				if currTemplate != nil {
					client.Channel() <- currTemplate
				}
				clients[client.ID] = &client
			}
		case "done":
			{
				return
			}
		}
	}
}

// handles templates and block submissions
func backendRoutine() {
	/// TODO: longpoll?
	/// TODO: do we need anything special for btcd/knots/etc?
	triggerGBT := make(chan bool)
	/// block submissions
	go func() {
		for {
			/// furst come furst serve
			submission, ok := <-submissionChan
			if !ok {
				logError("{yellow}failed to receive block submission")
				continue
			}
			err := backend.SubmitBlock(submission.Block, nil)
			if err != nil {
				logError("error from backend while submitting block: %s", err)
				continue
			}
			worker := clients[submission.ClientID].Name()
			log(
				"{green}=={yellow}[!]{green}==<BLOCK FOUND>=={yellow}[!]{green}==<BLOCK FOUND>=={yellow}[!]{green}==<BLOCK FOUND>=={yellow}[!]{green}==\nhash: %s\nworker: %s",
				submission.Block.Hash(),
				worker,
			)

			triggerGBT <- true
		}
	}()
	/// poll getblockcount
	if conf.Backend.Websocket {
		/// FIXME: doesnt work :c
		if err := backend.NotifyBlocks(); err != nil {
			cli.Exit(err.Error(), constants.ERROR_BACKEND)
			return
		}
	} else {
		go func() {
			/// needs to start after the gbt loop
			waitForTemplate()
			for {
				count, err := backend.GetBlockCount()
				if err != nil {
					logError("%s", err)
				}
				/// we're mining on this height
				if count == currTemplate.Height {
					log("===<there are now {green}%d{cyan} bl00ks in the chain!>===", count)
					triggerGBT <- true
				}
				time.Sleep(time.Millisecond * time.Duration(conf.Backend.PollInterval))
			}
		}()
	}

	/// main gbt loop
	for {
		template, err := backend.GetBlockTemplate(&btcjson.TemplateRequest{
			Rules:        []string{"segwit"}, /// required by gbt
			Capabilities: []string{"proposal", "coinbasevalue", "longpoll"},
			Mode:         "template",
		})
		if err != nil {
			logError("error fetching template: %s", err)
			time.Sleep(time.Millisecond * time.Duration(conf.Backend.PollInterval))
			continue
		}
		currTemplate = CreateJobTemplate(template)
		log("===<the swarm is working on job {blue}%s{cyan}!>===\n\ttxns: {green}%d", currTemplate.ID, len(template.Transactions))
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

// util func, for delaying components that rely on the template like
// client subscribes and the chain update routine
func waitForTemplate() {
	for {
		if currTemplate != nil {
			break
		}
		time.Sleep(time.Millisecond * time.Duration(conf.Backend.PollInterval))
	}
}

// listens on one ip
func listenerRoutine(shutdown chan struct{}, conns chan net.Conn, listener net.Listener, httpAddr string) {
	defer listener.Close()
	log("stratum listening on {white}%s", listener.Addr())
	go http.ListenAndServe(httpAddr, nil)
	log("api listening on {white}%s", httpAddr)
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

func notifyClients(j *JobTemplate) {
	for _, client := range clients {
		client.Channel() <- j
	}
}

func log(s string, a ...any) {
	fmt.Println(oigiki.ProcessTags(oigiki.TagString(s, "cyan"), a...))
}
func logError(s string, a ...any) {
	println(oigiki.ProcessTags(oigiki.TagString(s, "red"), a...))
}
