/*
	/pogolo - decentralize or die/

solo db-less bitcoin-only mining pool, meant for lan swarms, not the internet;
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

	--conf path, --config path, -c path  config file path (default: "$XDG_CONFIG_HOME/pogolo/pogolo.toml")
	--writedefaultconf path              write default config to path and exit
	--prof dir, --profile dir            write cpu and memory profiles to dir
	--help, -h                           show help
	--version, -v                        print the version
	--color                              force enable color output
	--nocolor                            force disable color output

environment overrides:

	POGOLO_HOST: # overrides [pogolo].host
	POGOLO_BACKEND_HOST: # overrides [backend].host
	POGOLO_BACKEND_RPCAUTH: # overrides [backend].rpcauth
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
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"git.0xf0xx0.eth.limo/0xf0xx0/pogolo/constants"

	"git.0xf0xx0.eth.limo/0xf0xx0/oigiki"
	"github.com/btcsuite/btcd/address/v2"
	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil/v2"
	"github.com/btcsuite/btcd/chaincfg/v2"
	"github.com/btcsuite/btcd/rpcclient"
	"github.com/btcsuite/btcd/wire/v2"
	"github.com/pelletier/go-toml/v2"
	"github.com/urfave/cli/v3"
)

// name and version
const (
	NAME    = "pogolo"
	VERSION = "1.1.3"
)

const commandHelpTemplate = `Usage:
   {green}{{.Name}} {blue}[options]{/}

Options:{blue}
   {{range .VisibleFlags}}{{.String}}
   {{end}}{/}
Version:
   {green}v{{.Version}}{/green}

Home page: <https://git.0xf0xx0.eth.limo/0xf0xx0/{{.Name}}>
`

// global state
var (
	// config stuffs
	conf               Config
	backend            *rpcclient.Client
	backendChainParams *chaincfg.Params
	defaultMiningAddr  address.Address

	// runtime state
	clients          = &clientMap{} // map of active client ids to clients
	currTemplate     *JobTemplate
	currTemplateID   uint64
	currTemplateLock sync.RWMutex
	submissionChan   = make(chan blockSubmission, 3) // global cause it gets passed around :\
	triggerGBT       = make(chan struct{}, 1)        // ditto cause of websocket
	foundBlocks      = make([]string, 0, 3)          // not gonna bother mutexing this unless it becomes an issue
	serverStartTime  time.Time
	logFile          *os.File

	/// debug shit
	totalSharesPerSec = float64(0)
	disableLogs       = false /// used for tests
)

func main() {
	cli.RootCommandHelpTemplate = oigiki.ProcessTags(commandHelpTemplate)
	app := &cli.Command{
		Name:                   NAME,
		Version:                VERSION,
		Usage:                  "Decentralize or die",
		UseShortOptionHandling: true,
		MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{
			{
				Flags: [][]cli.Flag{
					{
						&cli.BoolFlag{
							Name:  "color",
							Usage: "force enable color output",
						},
					},
					{
						&cli.BoolFlag{
							Name:  "nocolor",
							Usage: "force disable color output",
						},
					},
				},
			},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "conf",
				Aliases: []string{"config", "c"},
				Usage:   "config file `path`",
				Value:   DEFAULT_CONFIG_PATH,
			},
			&cli.StringFlag{
				Name:  "writedefaultconf",
				Usage: "write default config to `path` and exit",
			},
			&cli.StringFlag{
				Name:    "prof",
				Aliases: []string{"profile"},
				Usage:   "write cpu and memory profiles to `dir`",
			},
			&cli.StringFlag{
				Name:  "logfile",
				Usage: "write logs to `file`",
			},

			// env config overrides, same format as their related keys
			&cli.StringFlag{
				Name:    "POGOLO_HOST",
				Sources: cli.EnvVars("POGOLO_HOST"),
				Hidden:  true,
			},
			&cli.StringFlag{
				Name:    "BACKEND_HOST",
				Sources: cli.EnvVars("POGOLO_BACKEND_HOST"),
				Hidden:  true,
			},
			&cli.StringFlag{
				Name:    "BACKEND_RPCAUTH",
				Sources: cli.EnvVars("POGOLO_BACKEND_RPCAUTH"),
				Hidden:  true,
			},
			&cli.StringFlag{
				Name:    "BACKEND_ZMQHOST",
				Sources: cli.EnvVars("POGOLO_BACKEND_ZMQHOST"),
				Hidden:  true,
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
			if cmd.Bool("color") {
				oigiki.NoColor = false
			} else if cmd.Bool("nocolor") {
				oigiki.NoColor = true
			}

			if cmd.String("writedefaultconf") != "" {
				WriteDefaultConfig(cmd.String("writedefaultconf"))
				return nil
			}
			/// set defaults
			DeepCopyConfig(&conf, &DEFAULT_CONFIG)
			if passedConfig := cmd.String("conf"); passedConfig != "" && passedConfig != "none" {
				/// overwrite with user conf
				if err := LoadConfig(passedConfig, &conf); err != nil {
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

			/// conf overrides
			if host := cmd.String("BACKEND_HOST"); host != "" {
				conf.Backend.Host = host
			}
			if auth := cmd.String("BACKEND_RPCAUTH"); auth != "" {
				conf.Rpcauth = auth
			}
			if host := cmd.String("POGOLO_HOST"); host != "" {
				conf.Pogolo.Host = host
			}
			if zmq := cmd.String("BACKEND_ZMQHOST"); zmq != "" {
				conf.ZMQHost = zmq
			}

			/// conf loading
			if path := cmd.String("logfile"); path != "" {
				conf.LogFile = resolvePath(path)

				file, err := os.Create(conf.LogFile)
				if err != nil {
					return err
				}
				logFile = file
			}
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

			/// ignore vardiff and diff suggestions when benching
			if conf.Benchmarking {
				conf.DisableVarDiff = true
				conf.IgnoreSuggDiff = true
				log("{bold}{yellow}==<<!>=<<!>=<<!>>=<benchmarking>=<<!>=<<!>=<<!>>==")
				log("{yellow}connect with a client to start")
			}

			/// init backend
			backendConnConf := &rpcclient.ConnConfig{
				Host:         conf.Backend.Host,
				DisableTLS:   true,
				HTTPPostMode: !conf.Websocket,
			}
			if conf.Websocket {
				backendConnConf.Endpoint = "ws"
			}
			if conf.Cookie != "" {
				backendConnConf.CookiePath = conf.Cookie
			} else if conf.Rpcauth != "" && strings.Contains(conf.Rpcauth, ":") {
				auth := strings.Split(conf.Rpcauth, ":")
				backendConnConf.User = auth[0]
				backendConnConf.Pass = auth[1]
			} else {
				return cli.Exit("did you forget to configure the backend auth? (no auth found)", constants.EXIT_CONFIG)
			}
			var err error
			backend, err = rpcclient.New(backendConnConf, &rpcclient.NotificationHandlers{
				/// we do not care
				/// FIXME: this makes gbt fail and cause a hang randomly?
				/// FIXME: stalls shutdown on certain errors? prolly related to longpolling
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
					backendChainParams = &chaincfg.MainNetParams
				}
			case "test":
				{
					backendChainParams = &chaincfg.TestNet3Params
				}
			case "testnet4":
				{
					backendChainParams = &chaincfg.TestNet4Params
				}
			case "regtest":
				{
					backendChainParams = &chaincfg.RegressionNetParams
				}
			case "signet":
				{
					backendChainParams = &chaincfg.SigNetParams
				}
			default:
				{
					return cli.Exit(fmt.Sprintf("what's a %q? (unknown backend chain)", mininginfo.Chain), constants.EXIT_BACKEND)
				}
			}

			/// decode the default mining address
			if conf.PoolAddress != "" {
				addr, err := address.DecodeAddress(conf.PoolAddress, backendChainParams)
				if err != nil {
					return cli.Exit(err.Error(), constants.EXIT_CONFIG)
				}
				defaultMiningAddr = addr
				log(fmt.Sprintf("default mining address configured! mining to {green}%s", conf.PoolAddress))
			}

			/// start
			log(fmt.Sprintf("===<<{bold}{blue}%s {green}v%s{/green} - %s{/blue}{/bold}>>===", cmd.Name, cmd.Version, cmd.Usage))
			log(fmt.Sprintf("mining on {green}%s", backendChainParams.Name))
			if conf.Sv1Password != "" {
				log(fmt.Sprintf("%q set as stratum v1 password", conf.Sv1Password))
			}
			return startup(rootCtx)
		},
	}

	/// no need to handle any errors here, ExitErrHandler will do it
	app.Run(context.Background(), os.Args)
}

func startup(rootCtx context.Context) error {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}
	defer wg.Wait()

	ctx, cancel := context.WithCancel(rootCtx)
	defer cancel()

	/// ws shutdown handler
	if conf.Backend.Websocket {
		defer func() {
			log("closing websocket")
			backend.Shutdown()
			backend.WaitForShutdown()
		}()
	}

	conns := make(chan net.Conn)

	/// init
	clients.Init()
	initAPI()

	wg.Go(func() { backendRoutine(ctx) })
	wg.Go(func() { connectionRoutine(conns, ctx) })

	/// start listening on configured interface or ip
	if conf.Interface != "" {
		inter, err := net.InterfaceByName(conf.Interface)
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
			addr := strings.Split(addr.String(), "/")[0]
			if strings.HasPrefix(addr, "fe80::") {
				addr += "%" + inter.Name
			}
			err = spawnListenerRoutine(ctx, addr, wg, conns)
			if err != nil {
				return err
			}
		}
	} else {
		/// is domain, lookup and listen on addrs
		if net.ParseIP(conf.Pogolo.Host) == nil {
			addrs, err := net.LookupHost(conf.Pogolo.Host)
			if err != nil {
				return cli.Exit(fmt.Sprintf("error looking up host %q: %s", conf.Pogolo.Host, err), constants.EXIT_NET)
			}
			for _, addr := range addrs {
				err = spawnListenerRoutine(ctx, addr, wg, conns)
				if err != nil {
					return err
				}
			}
		} else {
			err := spawnListenerRoutine(ctx, conf.Pogolo.Host, wg, conns)
			if err != nil {
				return err
			}
		}
	}

	serverStartTime = time.Now()

	// wait for exit
	<-sigs

	log("\n{yellow}stopping")
	if conf.Benchmarking {
		println(fmt.Sprintf("total shares/s: %f", totalSharesPerSec))
	}
	return nil
}

func spawnListenerRoutine(ctx context.Context, addr string, wg *sync.WaitGroup, conns chan<- net.Conn) error {
	listener, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(int(conf.Port))))
	if err != nil {
		return cli.Exit(fmt.Sprintf("error listening on addr %q: %s", addr, err), constants.EXIT_NET)
	}
	httpAddr := net.JoinHostPort(addr, strconv.Itoa(int(conf.HTTPPort)))
	wg.Go(func() {
		listenerRoutine(conns, listener, httpAddr, ctx)
	})
	return nil
}

// listens on one ip and sends connections down `conns`
func listenerRoutine(conns chan<- net.Conn, listener net.Listener, httpAddr string, ctx context.Context) {
	defer listener.Close()
	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	go http.ListenAndServe(httpAddr, nil)
	// log(fmt.Sprintf("stratum listening on {green}stratum+tcp://%s{/green} (api port: {green}%d{/green})", listener.Addr(), conf.HTTPPort))
	log(fmt.Sprintf("stratum listening on {green}stratum+tcp://%s{/green}\napi listening on {green}http://%s", listener.Addr(), httpAddr))
	// log(fmt.Sprintf("api listening on {green}http://%s", httpAddr))

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

// handles clients
func connectionRoutine(conns <-chan net.Conn, ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			{
				return
			}
		case conn := <-conns:
			{
				/// no need for a pool, pogolo will likely never handle enough clients for it to matter
				client := CreateClient(conn, submissionChan)
				go client.Run(ctx)
			}
		}
	}
}

// handles templates, block notifications, and block submissions
// FIXME: add longpolling to rpcclient
func backendRoutine(ctx context.Context) {
	// longpollid := ""
	getBlockCountPoll := func() {
		/// wait for the initial template
	busywait:
		/// wait for furst template to be made...
		for {
			select {
			case <-ctx.Done():
				{
					return
				}
			case <-time.After(time.Millisecond * time.Duration(conf.PollInterval)):
				{
					currTemplateLock.RLock()
					if currTemplate != nil {
						currTemplateLock.RUnlock()
						break busywait
					}
					currTemplateLock.RUnlock()
				}
			}
		}

		/// ...then update on height changes
		for {
			select {
			case <-ctx.Done():
				{
					return
				}
			case <-time.After(time.Millisecond * time.Duration(conf.PollInterval)):
				{
					if count, err := backend.GetBlockCount(); err == nil {
						currTemplateLock.RLock()
						if count >= currTemplate.Height {
							triggerGBT <- struct{}{}
						}
						currTemplateLock.RUnlock()
					} else {
						logError(err.Error())
					}
				}
			}
		}
	}

	/// block notifications
	if conf.Backend.Websocket {
		/// wait for new blocks to come in
		if err := backend.NotifyBlocks(); err != nil {
			logError(fmt.Sprintf("error subscribing to block notifs: %s", err))
			logError("{yellow}falling back to polling")
			go getBlockCountPoll()
		}
	} else if conf.ZMQHost != "" {
		// implicitly add tcp:// as required by zmq4
		if !strings.HasPrefix(conf.ZMQHost, "tcp://") {
			conf.ZMQHost = "tcp://" + conf.ZMQHost
		}
		socket, err := newZMQ(conf.ZMQHost, ctx)
		if err != nil {
			logError(fmt.Sprintf("failed to make zmq socket: %s", err))
			logError("{yellow}falling back to polling")
			go getBlockCountPoll()
		} else {
			log(fmt.Sprintf("connected to zmq at {green}%s", conf.ZMQHost))
			/// go, my zmq
			go zmqListener(socket)
		}
	} else {
		/// poll getblockcount
		go getBlockCountPoll()
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
					block := btcutil.NewBlock(currTemplate.MsgBlock.Copy())
					msgBlock := block.MsgBlock()
					msgBlock.Header = submission.Header
					msgBlock.Transactions[0] = submission.Coinbase

					err := backend.SubmitBlock(block, nil)
					if err != nil {
						logError(fmt.Sprintf("error from backend while submitting block: %s", err))
						continue
					}
					client, _ := clients.Get(submission.ClientID)
					shareHash := msgBlock.Header.BlockHash()
					shareDiff := calcDifficulty(shareHash)
					foundBlocks = append(foundBlocks, shareHash.String())
					log(fmt.Sprintf(
						"{bold}{green}=={yellow}[!]{/yellow}==<BL00K FOUND>=={yellow}[!]{/yellow}==<BL00K FOUND>=={yellow}[!]{/yellow}==<BL00K FOUND>=={yellow}[!]{/yellow}=={/bold}\n{/green}gopher: {green}%s{/green}\nhash: {green}%s{/green}\ndifficulty: {green}%s{/green}\nnonce: {green}%08x{/green}\nextranonce: {blue}%s{green}%x",
						client.Name(),
						shareHash,
						formatDifficulty(shareDiff),
						submission.Share.Nonce,
						client.ID,
						submission.Share.Extranonce2,
					))
					// log(fmt.Sprintf("%d %d", seeda, seedb))

					/// one day pogolo will win a block, reload, and win a second back to back
					/// im manifesting it now
					triggerGBT <- struct{}{}
				}
			}
		}
	}()

	// queue initial job template fetch
	triggerGBT <- struct{}{}

	/// main gbt loop
	for {
		select {
		case <-ctx.Done():
			{
				return
			}
		case <-time.After(time.Second * time.Duration(conf.JobInterval)):
		/// shortcircuit
		case <-triggerGBT:
		}

		template, err := backend.GetBlockTemplate(&btcjson.TemplateRequest{
			Rules:        []string{"segwit"}, /// required by gbt
			Capabilities: []string{"proposal", "coinbasevalue" /* "longpoll" */},
			Mode:         "template",
			// LongPollID:   longpollid,
		})
		if err != nil {
			logError(fmt.Sprintf("error fetching template: %s", err))
			continue
		}

		// t, _ := sonic.MarshalString(&template)
		// log(t)

		/// save longpoll id
		// longpollid = template.LongPollID

		currTemplateLock.Lock()
		jobTemplate, err := CreateJobTemplate(template)
		if err != nil {
			logError(fmt.Sprintf("error making job template: %s", err.Error()))
			currTemplateLock.Unlock()
			continue
		}
		if currTemplate != nil && jobTemplate.Height > currTemplate.Height {
			log(fmt.Sprintf("==//==<there are now {blue}%d{/blue} bl00ks in the chain!>==//==", currTemplate.Height))
		}
		currTemplate = jobTemplate
		currTemplateLock.Unlock()
		log(fmt.Sprintf("==//==<the dig is mining on job {blue}%#x{/blue}!>==//==\n\ttxns: {blue}%d", currTemplate.ID, len(template.Transactions)))
		/// this gets shipped to each StratumClient to become a full MiningJob
		go clients.NotifyAll(jobTemplate) /// this might take a while
	}
}
