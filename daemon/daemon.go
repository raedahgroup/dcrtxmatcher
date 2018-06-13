package daemon

import (
	"fmt"
	"net"

	pb "github.com/raedahgroup/dcrtxmatcher/api/matcherrpc"
	"github.com/raedahgroup/dcrtxmatcher/matcher"
	"google.golang.org/grpc"
)

// Daemon is the main instance of a running dcr split ticket matcher daemon
type Daemon struct {
	cfg     *Config
	matcher *matcher.Matcher
}

// NewDaemon returns a new daemon instance and prepares it to listen to
// requests.
func NewDaemon(cfg *Config) (*Daemon, error) {
	d := &Daemon{
		cfg: cfg,
	}

	mcfg := &matcher.Config{
		MinParticipants: cfg.MinParticipants,
		RandomIndex:     cfg.RandomIndex,
		JoinTicker:      cfg.JoinTicker,
		WaitingTimer:    cfg.WaitingTimer,
	}
	//set matcher config
	d.matcher = matcher.NewMatcher(mcfg)
	return d, nil
}

// ListenAndServe connections for the daemon. Returns an error when done.
func (daemon *Daemon) ListenAndServe() error {
	intf := fmt.Sprintf(":%d", daemon.cfg.Port)

	lis, err := net.Listen("tcp", intf)
	if err != nil {
		log.Errorf("Error listening: %v", err)
		return err
	}

	go daemon.matcher.Run()

	server := grpc.NewServer()
	pb.RegisterSplitTxMatcherServiceServer(server, NewSplitTxMatcherService(daemon.matcher))

	log.Debugf("Listening on %s", intf)
	server.Serve(lis)

	return nil
}
