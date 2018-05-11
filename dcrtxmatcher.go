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

	log.Debugf("maxparti %d", config.MaxParticipants)

	cfg := &daemon.Config{
		MaxParticipants: config.MaxParticipants,
		Port:            config.Port,
	}
	daemon, err := daemon.NewDaemon(cfg)
	if err != nil {
		panic(err)
	}

	daemon.ListenAndServe()
}
