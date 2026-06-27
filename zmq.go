package main

import (
	"context"
	"fmt"
	"time"

	"github.com/go-zeromq/zmq4"
)

// TODO: figure out how to make conn re-sub on reconnect
func NewZMQ(endpoint string, ctx context.Context) (zmq4.Socket, error) {
	socket := zmq4.NewSub(ctx, zmq4.WithAutomaticReconnect(true),
		zmq4.WithDialerRetry(time.Duration(conf.PollInterval)),
		zmq4.WithDialerTimeout(3*time.Second),
		zmq4.WithTimeout(3*time.Second),
	)
	socket.SetOption(zmq4.OptionSubscribe, "hashblock")
	socket.SetOption(zmq4.OptionHWM, 0)

	if err := socket.Dial(endpoint); err != nil {
		return nil, err
	}
	return socket, nil
}
func zmqListener(socket zmq4.Socket) {
	for {
		msg, err := socket.Recv()
		if err != nil {
			if err == zmq4.ErrClosedConn || err == context.Canceled {
				break
			}
			logError(fmt.Sprintf("failed to receive zmq message: %s", err))
			continue
		}
		/// NOTE: bitcoind zmq can use the same address for multiple topics
		/// ensure we only trigger gbt on hashblock
		if string(msg.Frames[0]) != "hashblock" {
			continue
		}
		triggerGBT <- struct{}{}
	}
}
