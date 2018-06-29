package main

import (
	//"context"
	"os"

	"context"
	"fmt"
	"net"

	pb "github.com/raedahgroup/dcrtxmatcher/api/matcherrpc"
	"github.com/raedahgroup/dcrtxmatcher/matcher"
	"google.golang.org/grpc"
)

func main() {
	// Create a context that is cancelled when a shutdown request is received
	// through an interrupt signal or an RPC request.
	ctx := withShutdownCancel(context.Background())
	go shutdownListener()

	if err := run(ctx); err != nil && err != context.Canceled {
		log.Info("Shutdown...")
		os.Exit(1)
	} else {
		log.Info("Shutdown..")
	}
}

func run(ctx context.Context) error {

	config, _, err := loadConfig(ctx)
	if err != nil {
		log.Errorf("loadConfig error %v", err)
		return err
	}

	log.Infof("MinParticipants %d", config.MinParticipants)

	mcfg := &matcher.Config{
		MinParticipants: config.MinParticipants,
		RandomIndex:     config.RandomIndex,
		JoinTicker:      config.JoinTicker,
		WaitingTimer:    config.WaitingTimer,
	}

	if done(ctx) {
		return ctx.Err()
	}
	//set matcher config
	ticketJoiner := matcher.NewTicketJoiner(mcfg)

	intf := fmt.Sprintf(":%d", config.Port)

	lis, err := net.Listen("tcp", intf)
	if err != nil {
		log.Errorf("Error listening: %v", err)
		return err
	}

	if done(ctx) {
		return ctx.Err()
	}
	go ticketJoiner.Run()

	server := grpc.NewServer()
	pb.RegisterSplitTxMatcherServiceServer(server, NewSplitTxMatcherService(ticketJoiner))

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
