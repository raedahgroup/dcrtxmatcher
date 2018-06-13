package main

import (
	"context"

	"github.com/raedahgroup/dcrtxmatcher/daemon"
)

func main() {
	// Create a context that is cancelled when a shutdown request is received
	// through an interrupt signal or an RPC request.
	ctx := withShutdownCancel(context.Background())
	config, _, err := loadConfig(ctx)
	if err != nil {
		log.Errorf("loadConfig error %v", err)
		return
	}

	log.Infof("MinParticipants %d", config.MinParticipants)

	cfg := &daemon.Config{
		MinParticipants: config.MinParticipants,
		Port:            config.Port,
		RandomIndex:     config.RandomIndex,
		JoinTicker:      config.JoinTicker,
		WaitingTimer:    config.WaitingTimer,
	}
	daemon, err := daemon.NewDaemon(cfg)
	if err != nil {
		panic(err)
	}

	daemon.ListenAndServe()
}
