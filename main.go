/*
	/pogolo - decentralize or die/

solo db-less bitcoin-only mining pool,
meant for lan swarms, not the internet;
start it, point your miners to it, and watch the logs roll by

think of this as public-pool but minimal and for self-sovereign nerds

infinite thanks to [github.com/btcsuite/btcd] for bitcoin tooling and public-pool for reference

setup:

	mkdir $XDG_CONFIG_HOME/pogolo
	pogolo --writedefaultconf $XDG_CONFIG_HOME/pogolo/pogolo.toml # or copy pogolo.example.toml to `$XDG_CONFIG_HOME/pogolo/pogolo.toml`
	# then configure the interface, backend, and auth

usage:

	pogolo [options]

options:

	--conf path              config file path (default: "$XDG_CONFIG_HOME/.config/pogolo/pogolo.toml")
	--writedefaultconf path  write default config to path and exit
	--profile dir            write cpu and memory profiles to dir
	--help, -h               show help
	--version, -v            print the version
*/
package main

import (
	"context"
	"errors"
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
	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/rpcclient"
	"github.com/btcsuite/btcd/wire"
	"github.com/pelletier/go-toml/v2"
	"github.com/urfave/cli/v3"
)

// things
const (
	NAME    = "pogolo"
	VERSION = "0.0.10"
)

// global state
var (
	conf              config.Config
	backend           *rpcclient.Client
	longpollid        string
	activeChainParams *chaincfg.Params
	defaultMiningAddr *btcutil.Address
	clients           = &clientMap{} // map of active client ids to clients
	currTemplateID    uint64
	currTemplate      *JobTemplate
	submissionChan    = make(chan blockSubmission, 3) // global cause it gets passed around :\
	triggerGBT        = make(chan struct{})           // ditto cause of websocket
	serverStartTime   time.Time
)

func main() {
	cli.RootCommandHelpTemplate = oigiki.ProcessTags(`Name:
    {bold}{blue}{{.Name}} - {{.Usage}}{/}

Usage:
    {green}pogolo {blue}[options]{/}

Options:{blue}
    {{range .VisibleFlags}}{{.String}}
    {{end}}{/}
Version:
    {green}{{.Version}}
`)
	app := &cli.Command{
		Name:                   NAME,
		Version:                VERSION,
		Usage:                  "Decentralize or die",
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
			&cli.StringFlag{
				Name:  "profile",
				Usage: "write cpu and memory profiles to `dir`",
			},
		},
		ExitErrHandler: func(_ context.Context, _ *cli.Command, err error) {
			logError(err.Error())
			var ec cli.ExitCoder
			if errors.As(err, &ec) {
				os.Exit(ec.ExitCode())
			}
			os.Exit(constants.EXIT_MISC)
		},
		Action: func(rootCtx context.Context, cmd *cli.Command) error {
			if profileDir := cmd.String("profile"); profileDir != "" {
				log(fmt.Sprintf("{bold}{yellow}==<<!>=<<!>=<<!>>=<profiling>=<<!>=<<!>=<<!>>==\nwriting cpu.prof and mem.prof to: {green}%s", profileDir))
				profileFile, err := os.Create(filepath.Join(profileDir, "./cpu.prof"))
				if err != nil {
					return err
				}
				memProfFile, err := os.Create(filepath.Join(profileDir, "./mem.prof"))
				if err != nil {
					return err
				}
				pprof.StartCPUProfile(profileFile)
				defer pprof.WriteHeapProfile(memProfFile)
				defer pprof.StopCPUProfile()
			}
			if cmd.String("writedefaultconf") != "" {
				config.WriteDefaultConfig(cmd.String("writedefaultconf"))
				return nil
			}

			/// set defaults
			config.DeepCopyConfig(&conf, &config.DEFAULT_CONFIG)
			if passedConfig := cmd.String("conf"); passedConfig != "" && passedConfig != "none" {
				/// overwrite with user conf
				if err := config.LoadConfig(passedConfig, &conf); err != nil {
					/// dont like that i have to do these but oki
					decodeErr := &toml.DecodeError{}
					strictErr := &toml.StrictMissingError{}
					if errors.As(err, &decodeErr) {
						return cli.Exit(fmt.Sprintf("error decoding config:\n%s", decodeErr.String()), constants.EXIT_CONFIG)
					} else if errors.As(err, &strictErr) {
						return cli.Exit(fmt.Sprintf("unknown keys in config:\n%s", strictErr.String()), constants.EXIT_CONFIG)
					}
					/// fs error
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
				/// FIXME: this makes gbt fail and cause a hang randomly?
				/// FIXME: stalls shutdown on certain errors?
				OnFilteredBlockConnected: func(height int32, _ *wire.BlockHeader, _ []*btcutil.Tx) {
					/// ok we kinda care
					log(fmt.Sprintf("==//==<there are now {blue}%d{/blue} bl00ks in the chain!>==//==", height))
					triggerGBT <- struct{}{}
				},
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
			if conf.Pogolo.PoolAddress != "" {
				addr, err := btcutil.DecodeAddress(conf.Pogolo.PoolAddress, activeChainParams)
				if err != nil {
					return cli.Exit(err.Error(), constants.EXIT_CONFIG)
				}
				defaultMiningAddr = &addr
				log(fmt.Sprintf("default mining address configured! mining to {green}%s", conf.Pogolo.PoolAddress))
			}

			/// start
			log(fmt.Sprintf("===<<{bold}{blue}%s {green}v%s{/green} - %s{/blue}{/bold}>>===", cmd.Name, cmd.Version, cmd.Usage))
			log(fmt.Sprintf("mining on {green}%s", activeChainParams.Name))
			return startup(rootCtx)
		},
	}

	/// no need to handle any errors here, ExitErrHandler will do it
	app.Run(context.Background(), os.Args)
}

func startup(rootCtx context.Context) error {
	ctx, cancel := context.WithCancel(rootCtx)
	wg := sync.WaitGroup{}
	sigs := make(chan os.Signal, 1)

	conns := make(chan net.Conn)
	clients.Init()

	initAPI()

	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go backendRoutine(ctx)

	/// start listening on configured interface or ip
	if conf.Pogolo.Interface != "" {
		inter, err := net.InterfaceByName(conf.Pogolo.Interface)
		if err != nil {
			return cli.Exit(fmt.Sprintf("error getting interface: %s", err), constants.EXIT_NET)
		}
		/// error if interface is down
		if inter.Flags&(net.FlagUp|net.FlagRunning) == 0 {
			return cli.Exit("the chosen interface isnt up and running!", constants.EXIT_NET)
		}
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
			go listenerRoutine(conns, listener, net.JoinHostPort(addr, strconv.Itoa(int(conf.Pogolo.HTTPPort))), ctx)
		}
	} else {
		/// TODO: use net.LookupHost for domains?
		listener, err := net.Listen("tcp", net.JoinHostPort(conf.Pogolo.IP, strconv.Itoa(int(conf.Pogolo.Port))))
		if err != nil {
			return cli.Exit(fmt.Sprintf("error listening: %s", err), constants.EXIT_NET)
		}
		go listenerRoutine(conns, listener, net.JoinHostPort(conf.Pogolo.IP, strconv.Itoa(int(conf.Pogolo.HTTPPort))), ctx)
	}

	/// connections
	go func() {
		defer wg.Done()
		wg.Add(1)
		for {
			select {
			case <-ctx.Done():
				{
					return
				}
			case conn := <-conns:
				{
					/// no need for a pool, pogolo will likely never handle enough clients for it to matter
					go clientHandler(conn, ctx)
				}
			}
		}
	}()

	serverStartTime = time.Now()
	// wait for exit
	<-sigs
	log("\n{yellow}stopping")
	cancel()
	if conf.Backend.Websocket {
		log("closing websocket")
		backend.Shutdown()
		backend.WaitForShutdown()
	}
	log("waiting for routines")
	wg.Wait()
	return nil
}

// handles individual conns, spawned as a goroutine
// TODO: refactor?
func clientHandler(conn net.Conn, ctx context.Context) {
	/// don't need to close the conn here, handled by client.Stop()

	client := CreateClient(conn, submissionChan)
	channel := client.MsgChannel()

	/// remove ourselves from the client map on disconnect
	defer func() {
		if client.ID != 0 {
			clients.Delete(client.ID)
		}
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
				clients.Add(&client)
			}
		}
	}
}

// handles templates, block notifications, and block submissions
func backendRoutine(ctx context.Context) {
	/// block notifications
	/// TODO: do we need anything special for btcd/knots/etc?
	if conf.Backend.Websocket {
		/// TODO: fallback to polling if err
		/// wait for new blocks to come in
		if err := backend.NotifyBlocks(); err != nil {
			cli.Exit(fmt.Sprintf("error subscribing to block notifs: %s", err), constants.EXIT_BACKEND)
			return
		}
	} else {
		/// poll getblockcount
		go func() {
			/// needs to start after the gbt loop
			waitForTemplate()
			for {
				count, err := backend.GetBlockCount()
				if err != nil {
					logError(err.Error())
				}
				/// we're mining on this height
				if count == currTemplate.Height {
					log(fmt.Sprintf("==//==<there are now {blue}%d{/blue} bl00ks in the chain!>==//==", count))
					/// FIXME: sometimes this double-triggers 3:< wait for the template to update before continuing
					triggerGBT <- struct{}{}
				}
				time.Sleep(time.Millisecond * time.Duration(conf.Backend.PollInterval))
			}
		}()
	}

	/// block submissions
	go func() {
		for {
			/// furst come furst serve
			select {
			case <-ctx.Done():
				{
					return
				}
			case submission := <-submissionChan:
				{
					err := backend.SubmitBlock(&submission.Block, nil)
					if err != nil {
						logError(fmt.Sprintf("error from backend while submitting block: %s", err))
						continue
					}
					client := clients.Get(submission.ClientID)
					log(fmt.Sprintf(
						"{bold}{green}=={yellow}[!]{/yellow}==<BL00K FOUND>=={yellow}[!]{/yellow}==<BL00K FOUND>=={yellow}[!]{/yellow}==<BL00K FOUND>=={yellow}[!]{/yellow}=={/bold}\ngopher: %s\nhash: %s\ndifficulty: %f\nnonce: %x\nextranonce: %s %x",
						client.Name(),
						submission.Block.Hash(),
						CalcDifficulty(submission.Block.MsgBlock().Header),
						submission.Share.Nonce,
						client.ID,
						submission.Share.ExtraNonce2,
					))

					/// one day pogolo will win a block, reload, and win a second back to back
					/// im manifesting it now
					triggerGBT <- struct{}{}
				}
			}
		}
	}()

	/// main gbt loop
	for {
		template, err := backend.GetBlockTemplate(&btcjson.TemplateRequest{
			Rules:        []string{"segwit"}, /// required by gbt
			Capabilities: []string{"proposal", "coinbasevalue", "longpoll"},
			Mode:         "template",
			LongPollID:   longpollid,
		})
		if err != nil {
			logError(fmt.Sprintf("error fetching template: %s", err))
			/// FIXME: this doesnt trigger :\
			if err.Error() == "the client has been shutdown" {
				cli.Exit("rpc shutdown fail? emergency exit", constants.EXIT_MISC)
				return
			}
			time.Sleep(time.Millisecond * time.Duration(conf.Backend.PollInterval))
			continue
		}

		/// save longpoll id
		longpollid = template.LongPollID

		/// MAYBE: option to ignore empty templates?
		// if len(template.Transactions) == 0 {}
		currTemplate = CreateJobTemplate(template)
		log(fmt.Sprintf("==//==<the dig is mining on job {blue}0x%s{/blue}!>==//==\n\ttxns: {blue}%d", currTemplate.ID, len(template.Transactions)))
		/// this gets shipped to each StratumClient to become a full MiningJob
		go notifyClients(currTemplate) /// this might take a while
		select {
		case <-time.After(time.Second * time.Duration(conf.Pogolo.JobInterval)):
		/// shortcircuit
		case <-triggerGBT:
		case <-ctx.Done():
			{
				return
			}
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
func listenerRoutine(conns chan net.Conn, listener net.Listener, httpAddr string, ctx context.Context) {
	defer listener.Close()
	log(fmt.Sprintf("stratum listening on {green}stratum+tcp://%s", listener.Addr()))
	go http.ListenAndServe(httpAddr, nil)
	log(fmt.Sprintf("api listening on {green}%s", httpAddr))
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
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
	for _, client := range clients.All() {
		client.Channel() <- j
	}
}
