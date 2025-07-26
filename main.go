package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"path"
	"pogolo/stratumclient"
	"sync"
	"syscall"
	"time"

	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/rpcclient"
)

// / state
var (
	clients        map[string]stratumclient.StratumClient
	currTemplate   *stratumclient.JobTemplate
	submissionChan chan *btcutil.Block
)

func main() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	clients = make(map[string]stratumclient.StratumClient, 5)

	var wg sync.WaitGroup
	shutdown := make(chan struct{})
	conns := make(chan net.Conn)
	submissionChan = make(chan *btcutil.Block)
	listener, err := net.Listen("tcp", "127.0.0.1:5661")
	// listener, err := net.Listen("tcp", "10.42.0.1:3333")
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	/// listener
	go func() {
		defer wg.Done()
		println("srv start")
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-shutdown:
					{
						println("srv shutdown")
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
	}()
	/// connections
	go func() {
		defer wg.Done()
		println("conns start")
		for {
			select {
			case <-shutdown:
				{
					println("conns shutdown")
					return
				}
			case conn := <-conns:
				{
					go clientHandler(conn)
				}
			}
		}
	}()
	wg.Add(2)
	go backendRoutine()
	<-sigs
	println()
	println("closing")
	close(shutdown)
}
func clientHandler(conn net.Conn) {
	defer conn.Close()
	client := stratumclient.CreateClient(conn, submissionChan)
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
						fmt.Print(fmt.Sprintf("new client %q (%s)", client.ID, client.Addr()))
						/// i dont think the order matters, but lets send the current template
						/// before adding to the client map, just in case notifyClients gets
						/// called in between (and rapid-fires jobs)
						client.Channel() <- currTemplate
						clients[client.ID.String()] = client
					}
				case "done":
					{
						fmt.Print(fmt.Sprintf("client disconnect %q (%s)", client.ID, client.Addr()))
						return
					}
				}
			}
		}
	}
}

// handles templates and block submissions
func backendRoutine() {
	homedir, _ := os.UserHomeDir()
	/// TODO: longpoll?
	/// TODO: do we need anything special for btcd/knots/etc?
	backend, err := rpcclient.New(&rpcclient.ConnConfig{
		Host:                "127.0.0.1:18443",
		CookiePath:          path.Join(homedir, ".bitcoin/regtest/.cookie"),
		DisableTLS:          true,
		DisableConnectOnNew: true,
		HTTPPostMode:        true,
	}, nil)
	if err != nil {
		panic(err)
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
			}
			println("===== BLOCK FOUND ===== BLOCK FOUND ===== BLOCK FOUND =====")
			println(fmt.Sprintf("hash: %s", block.Hash().String()))
			/// TODO: trigger new job template?
		}
	}()
	for {
		template, err := backend.GetBlockTemplate(&btcjson.TemplateRequest{
			Rules: []string{"segwit"}, /// required by gbt
			Capabilities: []string{"proposal", "coinbasevalue" /*, "longpoll"*/},
			Mode:         "template",
		})
		if err != nil {
			panic(err)
		}
		println(fmt.Sprintf("new template with %d txns", len(template.Transactions)))
		/// this gets shipped to each StratumClient to become a full MiningJob
		currTemplate = stratumclient.CreateJobTemplate(template)
		go notifyClients(currTemplate) /// this might take a while
		/// TODO: 30s?
		time.Sleep(time.Minute)
	}
}

func notifyClients(n *stratumclient.JobTemplate) {
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
