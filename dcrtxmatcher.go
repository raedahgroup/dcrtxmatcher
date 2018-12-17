package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"

	//"time"

	_ "net/http/pprof"

	pb "github.com/decred/dcrwallet/dcrtxclient/api/matcherrpc"
	//"github.com/decred/dcrwallet/dcrtxclient/util"
	"github.com/gorilla/websocket"
	"github.com/raedahgroup/dcrtxmatcher/coinjoin"
	"github.com/raedahgroup/dcrtxmatcher/matcher"
	"google.golang.org/grpc"
)

func main() {
	go func() {
		http.ListenAndServe("localhost:6060", nil)
	}()
	// Create a context that is cancelled when a shutdown request is received
	// through an interrupt signal or an RPC request.
	ctx := withShutdownCancel(context.Background())
	go shutdownListener()

	if err := run(ctx); err != nil && err != context.Canceled {
		os.Exit(1)
	}
}

// run executes dcrtxmatcher server. Base on setting on config file
// server uses coinshuffle++ if blind server option is enabled, if not using normal coin join method.
func run(ctx context.Context) error {
	config, _, err := loadConfig(ctx)
	if err != nil {
		mtlog.Errorf("Can not load the config data: %v", err)
		return err
	}

	if done(ctx) {
		return ctx.Err()
	}

	// Within coinshuffle++, the dicemix protocol is used for the participants to exchange information.
	// Dicemix uses the flint library to solve polynomial to get the roots and the peer's output address.
	// The flint library is a required dependency and is the method that is suggested by the
	// authors of the coinshuffle++ paper.
	if config.BlindServer {
		log.Infof("MinParticipants %d", config.MinParticipants)
		cfg := &coinjoin.Config{JoinTicker: config.JoinTicker, RoundTimeOut: config.WaitingTimer, MinParticipants: config.MinParticipants}
		cfg.ServerPublish = config.ServerPublish
		diceMix := coinjoin.NewDiceMix(cfg)
		joinQueue := coinjoin.NewJoinQueue(config.JoinTicker)
		go diceMix.Run(joinQueue)

		// Create websocket server
		http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{
				ReadBufferSize:  0,
				WriteBufferSize: 0,
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				mtlog.Errorf("Can not upgrade from remote address %v", r.RemoteAddr)
				return
			}

			// Add websocket connection to peer data.
			peer := coinjoin.NewPeer(conn)
			peer.IPAddr = r.RemoteAddr
			joinQueue.NewPeerChan <- peer
		})

		if done(ctx) {
			return ctx.Err()
		}

		intf := fmt.Sprintf(":%d", config.Port)
		log.Infof("Listening on %s", intf)
		go func() {
			err := http.ListenAndServe(intf, nil)
			if err != nil {
				log.Errorf("Can not start server: %v", err)
				return
			}
		}()
	}

	// With normal coin join method, the server knows the output addresses of all participants.
	// After received transaction input and output from participants,
	// Server swaps randomly the transaction input, output index of all participants.
	// The joined transaction created from this then sent back to each participants to sign.
	if !config.BlindServer {
		mcfg := &matcher.Config{
			MinParticipants: config.MinParticipants,
			RandomIndex:     config.RandomIndex,
			JoinTicker:      config.JoinTicker,
			WaitingTimer:    config.WaitingTimer,
		}
		ticketJoiner := matcher.NewTicketJoiner(mcfg)
		joinQueue := matcher.NewJoinQueue()

		intf := fmt.Sprintf(":%d", config.Port)
		lis, err := net.Listen("tcp", intf)
		if err != nil {
			mtlog.Errorf("Error listening: %v", err)
			return err
		}

		if done(ctx) {
			return ctx.Err()
		}
		go ticketJoiner.Run(joinQueue)

		server := grpc.NewServer()
		pb.RegisterSplitTxMatcherServiceServer(server, NewSplitTxMatcherService(ticketJoiner, joinQueue))

		if done(ctx) {
			return ctx.Err()
		}
		mtlog.Infof("Listening on %s", intf)
		go func() {
			err := server.Serve(lis)
			if err != nil {
				mtlog.Errorf("Can not start server: %v", err)
				os.Exit(1)
			}
		}()

		if server != nil {
			defer func() {
				mtlog.Info("Stop Grpc server...")
				server.Stop()
				mtlog.Info("Grpc server stops")
			}()
		}

		if ticketJoiner != nil {
			defer func() {
				mtlog.Info("Stop Ticket joiner...")
				ticketJoiner.Stop(config.CompleteJoin)
				mtlog.Info("Ticket joiner stops")
			}()
		}
	}
	// Wait until shutdown is signaled before returning and running deferred
	// shutdown tasks.
	<-ctx.Done()

	return ctx.Err()
}

// done returns whether the context's Done channel was closed due to
// cancellation or exceeded deadline.
func done(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
