package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"pogolo/stratumclient"
	"sync"
	"syscall"
)

var (
	clients []any
)

func main() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	shutdown := make(chan struct{})
	conns := make(chan net.Conn)
	// listener, err := net.Listen("tcp", "127.0.0.1:5661")
	listener, err := net.Listen("tcp", "10.42.0.1:3333")
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	/// listener
	go func() {
		defer wg.Done()
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
	<-sigs
	println()
	println("closing")
	close(shutdown)
}
func clientHandler(conn net.Conn) {
	defer conn.Close()
	client := stratumclient.CreateClient(conn)
	go client.Run()
	channel := client.Channel()
	for {
		select {
		case msg := <-channel: {
			if msg == "ready" {
				fmt.Print(fmt.Sprintf("new client %q (%s)", client.ID, client.Addr()))
			}
		}
		}
	}
}
