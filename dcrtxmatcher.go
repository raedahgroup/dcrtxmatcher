package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/gorilla/websocket"

	pb "github.com/decred/dcrwallet/dcrtxclient/api/matcherrpc"
	"github.com/raedahgroup/dcrtxmatcher/coinjoin"
	"github.com/raedahgroup/dcrtxmatcher/matcher"
	"google.golang.org/grpc"
)

func main() {
	// Create a context that is cancelled when a shutdown request is received
	// through an interrupt signal or an RPC request.
	ctx := withShutdownCancel(context.Background())
	go shutdownListener()

	if err := run(ctx); err != nil && err != context.Canceled {
		os.Exit(1)
	}
}

func run(ctx context.Context) error {

	config, _, err := loadConfig(ctx)
	if err != nil {
		log.Errorf("loadConfig error %v", err)
		return err
	}

	if done(ctx) {
		return ctx.Err()
	}

	if config.BlindServer {
		dcmixlog.Infof("MinParticipants %d", config.MinParticipants)
		dicemixCfg := &coinjoin.Config{
			MinParticipants: config.MinParticipants,
			RandomIndex:     config.RandomIndex,
			JoinTicker:      config.JoinTicker,
			WaitingTimer:    config.WaitingTimer,
		}
		//websocket
		joinQueue := coinjoin.NewJoinQueue()

		http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {

			upgrader := websocket.Upgrader{
				ReadBufferSize:  1024 * 1024,
				WriteBufferSize: 1024 * 1024,
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				log.Errorf("Can not upgrade from remote address %v", r.RemoteAddr)
				return
			}

			//add ws connection to dicemix for management
			peer := coinjoin.NewPeer(conn)
			peer.IPAddr = r.RemoteAddr

			joinQueue.NewPeerChan <- peer

		})

		diceMix := coinjoin.NewDiceMix(dicemixCfg)
		go diceMix.Run(joinQueue)

		intf := fmt.Sprintf(":%d", config.Port)
		dcmixlog.Infof("Listening on %s", intf)

		go http.ListenAndServe(intf, nil)

	} else {
		mcfg := &matcher.Config{
			MinParticipants: config.MinParticipants,
			RandomIndex:     config.RandomIndex,
			JoinTicker:      config.JoinTicker,
			WaitingTimer:    config.WaitingTimer,
		}
		//set matcher config
		ticketJoiner := matcher.NewTicketJoiner(mcfg)

		waitingQueue := matcher.NewWaitingQueue()

		intf := fmt.Sprintf(":%d", config.Port)

		lis, err := net.Listen("tcp", intf)
		if err != nil {
			log.Errorf("Error listening: %v", err)
			return err
		}

		if done(ctx) {
			return ctx.Err()
		}
		go ticketJoiner.Run(waitingQueue)

		server := grpc.NewServer()
		pb.RegisterSplitTxMatcherServiceServer(server, NewSplitTxMatcherService(ticketJoiner, waitingQueue))

		if done(ctx) {
			return ctx.Err()
		}
		log.Infof("Listening on %s", intf)
		go server.Serve(lis)

		if server != nil {
			defer func() {
				log.Info("Stop Grpc server...")
				server.Stop()
				log.Info("Grpc server stops")
			}()
		}

		if ticketJoiner != nil {
			defer func() {
				log.Info("Stop Ticket joiner...")
				ticketJoiner.Stop(config.CompleteJoin)
				log.Info("Ticket joiner stops")
			}()
		}
	}

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
