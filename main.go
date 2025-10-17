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
	"strconv"
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
	VERSION = "0.0.8"
)

// state
var (
	conf              config.Config
	backend           *rpcclient.Client
	activeChainParams *chaincfg.Params
	defaultMiningAddr *btcutil.Address
	clients           map[stratum.ID]*StratumClient // map of client ids to clients
	currTemplate      *JobTemplate
	submissionChan    chan BlockSubmission
	serverStartTime   time.Time
)

func main() {
	app := &cli.Command{
		Name:                   NAME,
		Version:                VERSION,
		Usage:                  "Decentralize or die",
		UsageText:              "pogolo [options]",
		UseShortOptionHandling: true,
		EnableShellCompletion:  true,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "conf",
				Usage: "config file `path`",
				Value: filepath.Join(config.ROOT, "pogolo.toml"),
			},
			&cli.StringFlag{
				Name:  "writedefaultconf",
				Usage: "write default config to `path` and exit",
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
					return cli.Exit(fmt.Sprintf("error loading config: %s", err), constants.EXIT_CONFIG)
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
				return cli.Exit("did you forget to configure the backend auth? (no auth found)", constants.EXIT_CONFIG)
			}
			var err error
			backend, err = rpcclient.New(backendConnConf, &rpcclient.NotificationHandlers{
				/// we do not care
				OnFilteredBlockConnected:    func(_ int32, _ *wire.BlockHeader, _ []*btcutil.Tx) {},
				OnFilteredBlockDisconnected: func(_ int32, _ *wire.BlockHeader) {},
				/// only needed for the backend.NotifyBlocks() call later
			})
			if err != nil {
				return cli.Exit(fmt.Sprintf("failed to connect to backend: %s", err), constants.EXIT_BACKEND)
			}

			mininginfo, err := backend.GetBlockChainInfo()
			if err != nil {
				return cli.Exit(fmt.Sprintf("failed to get chain info: %s", err), constants.EXIT_BACKEND)
			}

			switch mininginfo.Chain {
			case "mainnet":
				fallthrough
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
					return cli.Exit(fmt.Sprintf("what's a %q? (unknown backend chain)", mininginfo.Chain), constants.EXIT_BACKEND)
				}
			}

			/// decode the default mining address
			if conf.Pogolo.ChainAddress != "" {
				addr, err := btcutil.DecodeAddress(conf.Pogolo.ChainAddress, activeChainParams)
				if err != nil {
					return cli.Exit(err.Error(), constants.EXIT_CONFIG)
				}
				defaultMiningAddr = &addr
				log(fmt.Sprintf("{blue}default mining address configured! mining to {green}%s", conf.Pogolo.ChainAddress))
			}
			/// start
			log(fmt.Sprintf("===<{bold}{blue}%s {green}v%s{/green} - %s{/blue}{/bold}>===", ctx.Name, ctx.Version, ctx.Usage))
			log(fmt.Sprintf("mining on {yellow}%s", activeChainParams.Name))
			return startup()
		},
	}
	if err := app.Run(context.Background(), os.Args); err != nil {
		logError(fmt.Sprintf("%s", err))
	}
}

func startup() error {
	wg := sync.WaitGroup{}
	sigs := make(chan os.Signal, 1)
	shutdown := make(chan struct{})

	conns := make(chan net.Conn)
	clients = make(map[stratum.ID]*StratumClient, 5)
	submissionChan = make(chan BlockSubmission, 3) /// buffered just in case, it doesnt hurt

	initAPI()

	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go backendRoutine()

	/// start listening on configured interface or ip
	if conf.Pogolo.Interface != "" {
		inter, err := net.InterfaceByName(conf.Pogolo.Interface)
		if err != nil {
			return cli.Exit(fmt.Sprintf("error getting interface: %s", err), constants.EXIT_NET)
		}
		/// FIXME
		// if inter.Flags&(net.FlagUp&net.FlagRunning) == 0 {
		// 	return cli.Exit("the chosen interface isnt up and/or running!", constants.EXIT_NET)
		// }
		addrs, err := inter.Addrs()
		if err != nil {
			return cli.Exit(fmt.Sprintf("error getting interface addrs: %s", err), constants.EXIT_NET)
		}
		if len(addrs) == 0 {
			return cli.Exit("the chosen interface has no addresses!", constants.EXIT_NET)
		}
		for _, addr := range addrs {
			/// trim bitmask or whatever its called
			addr := strings.Split(addr.String(), "/")[0]

			/// listen on the link-local too, if its there
			if strings.HasPrefix(addr, "fe80::") {
				addr += "%" + inter.Name
			}
			listener, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(int(conf.Pogolo.Port))))
			if err != nil {
				return cli.Exit(fmt.Sprintf("error listening on addr %q: %s", addr, err), constants.EXIT_NET)
			}
			go listenerRoutine(shutdown, conns, listener, net.JoinHostPort(addr, strconv.Itoa(int(conf.Pogolo.HTTPPort))))
		}
	} else {
		/// TODO: use net.LookupHost for domains?
		listener, err := net.Listen("tcp", net.JoinHostPort(conf.Pogolo.IP, strconv.Itoa(int(conf.Pogolo.Port))))
		if err != nil {
			return cli.Exit(fmt.Sprintf("error listening: %s", err), constants.EXIT_NET)
		}
		go listenerRoutine(shutdown, conns, listener, net.JoinHostPort(conf.Pogolo.IP, strconv.Itoa(int(conf.Pogolo.HTTPPort))))
	}

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
	/// remove ourselves from the client map on disconnect
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
				logError(fmt.Sprintf("error from backend while submitting block: %s", err))
				continue
			}
			worker := clients[submission.ClientID].Name()
			log(fmt.Sprintf(
					"{green}=={yellow}[!]{/yellow}==<BL00K FOUND>=={yellow}[!]{/yellow}==<BL00K FOUND>=={yellow}[!]{/yellow}==<BL00K FOUND>=={yellow}[!]{/yellow}==\nhash: %s\ndifficulty: %f\nworker: %s",
					submission.Block.Hash(),
					CalcDifficulty(submission.Block.MsgBlock().Header), /// TODO: pass the share info from the client?
					worker,
			))

			triggerGBT <- true
		}
	}()
	/// poll getblockcount
	if conf.Backend.Websocket {
		if err := backend.NotifyBlocks(); err != nil {
			cli.Exit(err.Error(), constants.EXIT_BACKEND)
			return
		}
	} else {
		go func() {
			/// needs to start after the gbt loop
			waitForTemplate()
			for {
				count, err := backend.GetBlockCount()
				if err != nil {
					logError(fmt.Sprintf("%s", err))
				}
				/// we're mining on this height
				if count == currTemplate.Height {
					log(fmt.Sprintf("===<there are now {blue}%d{/blue} bl00ks in the chain!>===", count))
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
			logError(fmt.Sprintf("error fetching template: %s", err))
			time.Sleep(time.Millisecond * time.Duration(conf.Backend.PollInterval))
			continue
		}
		currTemplate = CreateJobTemplate(template)
		log(fmt.Sprintf("===<the swarm is working on job {blue}0x%s{/blue}!>===\n\ttxns: {blue}%d", currTemplate.ID, len(template.Transactions)))
		/// this gets shipped to each StratumClient to become a full MiningJob
		go notifyClients(currTemplate) /// this might take a while
		select {
		case <-time.After(time.Second * time.Duration(conf.Pogolo.JobInterval)):
		/// shortcircuit
		case <-triggerGBT:
		}
	}
}

// util func, for delaying components that rely on the template like
// the chain update routine
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
	log(fmt.Sprintf("stratum listening on {white}%s", listener.Addr()))
	go http.ListenAndServe(httpAddr, nil)
	log(fmt.Sprintf("api listening on {white}%s", httpAddr))
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

func log(s string) {
	fmt.Println(oigiki.ProcessTags(oigiki.TagString(s, "cyan")))
}
func logError(s string) {
	println(oigiki.ProcessTags(oigiki.TagString(s, "red")))
}
