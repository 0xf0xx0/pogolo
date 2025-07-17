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
	"github.com/btcsuite/btcd/rpcclient"
)

// / state
var (
	clients      map[string]stratumclient.StratumClient
	currTemplate *stratumclient.JobTemplate
)

func main() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	shutdown := make(chan struct{})
	conns := make(chan net.Conn)
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
	go gbtRoutine()
	<-sigs
	println()
	println("closing")
	close(shutdown)
}
func clientHandler(conn net.Conn) {
	defer conn.Close()
	client := stratumclient.CreateClient(conn)
	channel := client.Channel()
	defer func() {
		delete(clients, client.ID.String())
	}()
	go client.Run()
	for {
		switch msg := (<-channel).(type) {
		case string:
			{
				if msg == "ready" {
					fmt.Print(fmt.Sprintf("new client %q (%s)", client.ID, client.Addr()))
					client.Channel() <- currTemplate
				}
			}
		}
	}
}

func gbtRoutine() {
	homedir, _ := os.UserHomeDir()
	client, err := rpcclient.New(&rpcclient.ConnConfig{
		Host:                "127.0.0.1:8332",
		CookiePath:          path.Join(homedir, ".bitcoin/rpc.cookie"),
		DisableTLS:          true,
		DisableConnectOnNew: true,
		HTTPPostMode:        true,
	}, nil)
	if err != nil {
		panic(err)
	}
	for {
		template, err := client.GetBlockTemplate(&btcjson.TemplateRequest{
			Rules:        []string{"segwit"}, /// required by gbt
			Capabilities: []string{"proposal", "coinbasevalue", "longpoll"},
			Mode:         "template",
		})
		println(fmt.Sprintf("new template with %d txns", len(template.Transactions)))
		if err != nil {
			panic(err)
		}
		/// this gets shipped to each StratumClient to become a full Job
		currTemplate = stratumclient.CreateJobTemplate(template)

		notifyClients(currTemplate)
		time.Sleep(time.Minute)
	}
}

func notifyClients(n any) {
	for _, client := range clients {
		go func(){client.Channel() <- n}()
	}
}
