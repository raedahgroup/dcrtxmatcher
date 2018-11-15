module github.com/raedahgroup/dcrtxmatcher

replace "github.com/decred/dcrwallet" master => "github.com/raedahgroup/dcrwallet" dicemix

require (
	github.com/ansel1/merry v1.3.1
	github.com/btcsuite/btclog v0.0.0-20170628155309-84c8d2346e9f
	github.com/decred/dcrd/dcrutil v1.1.1
	github.com/decred/dcrd/wire v1.1.0
	"github.com/decred/dcrwallet" master
	github.com/decred/dcrwallet/version v1.0.0
	github.com/decred/slog v1.0.0
	github.com/gogo/protobuf v1.1.1
	github.com/golang/protobuf v1.2.0
	github.com/gorilla/websocket v1.4.0
	github.com/huyntsgs/go-ecdh v0.0.0-20181109030121-b51cad457d92
	github.com/jessevdk/go-flags v1.4.0
	github.com/jrick/logrotate v1.0.0
	github.com/rs/xid v1.2.1
	golang.org/x/net v0.0.0-20181011144130-49bb7cea24b1
	google.golang.org/grpc v1.15.0
)
