package coinjoin

import (
	"crypto/rand"

	"math/big"
	mrand "math/rand"
	"sync"
	"time"

	"github.com/decred/dcrd/wire"
	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"

	pb "github.com/decred/dcrwallet/dcrtxclient/api/messages"
	"github.com/decred/dcrwallet/dcrtxclient/finitefield"
	"github.com/decred/dcrwallet/dcrtxclient/messages"
	"github.com/decred/dcrwallet/dcrtxclient/util"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 1024 * 1024
)

type (
	// PeerInfo contains data of peer.
	PeerInfo struct {
		Id          uint32
		SessionId   uint32
		Conn        *websocket.Conn
		JoinSession *JoinSession
		JoinQueue   *JoinQueue
		PK          []byte
		IPAddr      string
		cmd         int
		writeChan   chan []byte

		// Peer number of pkscripts ~ number of tickets purchase
		NumMsg uint32

		// Record peer's input index
		InputIndex []int
		Publisher  bool

		DcExpVector []field.Field
		DcXorVector [][]byte
		Commit      []byte

		TxIns       *wire.MsgTx
		SignedTx    *wire.MsgTx
		TicketPrice int64
	}

	JoinQueue struct {
		mu          sync.Mutex
		Peers       map[uint32]*PeerInfo
		NewPeerChan chan *PeerInfo
	}

	DiceMix struct {
		sessionTicker *time.Ticker
		Sessions      map[uint32]*JoinSession
		config        *Config
	}

	Config struct {
		MinParticipants int
		RandomIndex     bool
		JoinTicker      int
		RoundTimeOut    int
	}
)

func init() {
	mrand.Seed(time.Now().UnixNano())
}

// ResetData sets new id and session id for peer.
// Also reset other data to prepare for new session.
func (peer *PeerInfo) ResetData(id, sessionId uint32) {
	peer.Id = id
	peer.SessionId = sessionId

	peer.InputIndex = []int{}
	peer.DcExpVector = []field.Field{}
	peer.DcXorVector = [][]byte{}
	peer.PK = []byte{}
	peer.TxIns = nil
	peer.SignedTx = nil
	peer.Commit = []byte{}
}

// Run does join transaction in every 2 minutes (setting in config file).
// If there is enough peers for join transaction, creates new join session.
func (diceMix *DiceMix) Run(joinQueue *JoinQueue) {
	for {
		select {
		case peer := <-joinQueue.NewPeerChan:
			joinQueue.AddNewPeer(peer)

		case <-diceMix.sessionTicker.C:
			timeStartJoin := time.Now().Add(time.Second * time.Duration(diceMix.config.JoinTicker))
			queueSize := len(joinQueue.Peers)
			if queueSize == 0 {
				log.Info("Zero participant connected")
				log.Info("Will start next join session at", util.GetTimeString(timeStartJoin))
				continue
			}
			if queueSize < diceMix.config.MinParticipants {
				log.Infof("Number participants %d, will wait for minimum %d", queueSize, diceMix.config.MinParticipants)
				log.Info("Will start next join session at", util.GetTimeString(timeStartJoin))
				continue
			}

			joinQueue.mu.Lock()
			sessionId := GenId()
			joinSession := NewJoinSession(sessionId, diceMix.config.RoundTimeOut)
			log.Infof("Start coin join transaction - sessionId %v", sessionId)

			for id, peer := range joinQueue.Peers {
				peer.SessionId = sessionId
				joinSession.Peers[id] = peer
				peer.JoinQueue = nil
				peer.JoinSession = joinSession

				coinJoinRes := &pb.CoinJoinRes{
					PeerId:    peer.Id,
					SessionId: peer.SessionId,
				}

				data, err := proto.Marshal(coinJoinRes)
				if err != nil {
					log.Errorf("Can not marshal coinJoinRes: %v", err)
					break
				}

				message := messages.NewMessage(messages.S_JOIN_RESPONSE, data)
				peer.writeChan <- message.ToBytes()
			}

			// Init new queue for next incoming peers
			joinQueue.Peers = make(map[uint32]*PeerInfo)
			joinQueue.mu.Unlock()

			// Run the join session
			joinSession.Config = &Config{RoundTimeOut: diceMix.config.RoundTimeOut}
			go joinSession.run()

		}
	}
}

// NewJoinQueue creates new join queue.
func NewJoinQueue() *JoinQueue {
	return &JoinQueue{
		Peers:       make(map[uint32]*PeerInfo),
		NewPeerChan: make(chan *PeerInfo),
	}
}

// NewDiceMix returns new dicemix struct with the given config.
func NewDiceMix(config *Config) *DiceMix {

	// Log time will start join transaction
	timeStartJoin := time.Now().Add(time.Second * time.Duration(config.JoinTicker))
	log.Info("Will start join session at", util.GetTimeString(timeStartJoin))

	return &DiceMix{
		config:        config,
		Sessions:      make(map[uint32]*JoinSession),
		sessionTicker: time.NewTicker(time.Second * time.Duration(config.JoinTicker)),
	}
}

// AddNewPeer adds new requested peer to join queue.
func (joinQueue *JoinQueue) AddNewPeer(peer *PeerInfo) {

	joinQueue.mu.Lock()
	defer joinQueue.mu.Unlock()

	log.Infof("New peer connected %v - %v", peer.Id, peer.IPAddr)
	joinQueue.Peers[peer.Id] = peer
	peer.JoinQueue = joinQueue

	log.Infof("Number of waiting peers %v", len(joinQueue.Peers))

	// Listening for peer's incoming messages and waiting to write data
	go peer.ReadMessages()
	go peer.WriteMessages()
}

// RemovePeer removes peer from join queue.
func (joinQueue *JoinQueue) RemovePeer(peer *PeerInfo) {
	if joinQueue == nil {
		return
	}

	joinQueue.mu.Lock()
	defer joinQueue.mu.Unlock()
	delete(joinQueue.Peers, peer.Id)
	log.Infof("Removed peer %v - %v from join queue", peer.Id, peer.IPAddr)
}

// NewPeer creates new peer data with provided websocket connection.
func NewPeer(wsconn *websocket.Conn) *PeerInfo {
	return &PeerInfo{
		Id:        GenId(),
		Conn:      wsconn,
		writeChan: make(chan []byte),
	}
}

// WriteMessages writes data to peer's websocket.
func (peer *PeerInfo) WriteMessages() {
	for {
		select {
		case msg := <-peer.writeChan:
			err := peer.Conn.WriteMessage(websocket.BinaryMessage, msg)
			if err != nil {
				log.Errorf("Write messsage to socket error: %v", err)
				if peer.JoinSession != nil {

					log.Infof("Peer %v disconnected in session state %s", peer.Id, peer.JoinSession.getStateString())
					peer.JoinSession.mu.Lock()

					switch peer.JoinSession.State {
					case StateKeyExchange:
						// Just remove, ignore and continue.
						peer.JoinSession.removePeer(peer.Id)
					case StateDcExponential:
					case StateDcXor:
					case StateTxInput:
					case StateTxSign:
						// Consider malicious peer, remove and inform to others.
						peer.JoinSession.pushMaliciousInfo([]uint32{peer.Id})
					case StateTxPublish:
						if peer.JoinSession.Publisher == peer.Id {
							peer.JoinSession.removePeer(peer.Id)
							if len(peer.JoinSession.Peers) <= 1 {
								// Terminates fail
							}
							// Select other peer to publish transaction.
							joinTxMsg := messages.NewMessage(messages.S_TX_SIGN, []byte{0x00})
							i := 0
							randIndex := mrand.Intn(len(peer.JoinSession.Peers))
							for _, peerInfo := range peer.JoinSession.Peers {
								if i == randIndex {
									peer.JoinSession.Publisher = peerInfo.Id
									peerInfo.writeChan <- joinTxMsg.ToBytes()
									break
								}
								i++
							}
						}
					}
					peer.JoinSession.mu.Unlock()

				} else {
					peer.JoinQueue.RemovePeer(peer)
					log.Infof("Peer %v disconnected", peer.Id)
				}
				break
			}
		}
	}
}

// ReadMessages reads incoming data on peer's websocket and parses received data.
func (peer *PeerInfo) ReadMessages() {

	defer peer.Conn.Close()
	for {

		cmd, data, err := peer.Conn.ReadMessage()
		if err != nil {
			log.Errorf("Can not read data from websockett: %v", err)
			if peer.JoinSession != nil {
				// Peer may disconnected, remove from join session.
				// TODO: check status of joinsession and have proper process
				delete(peer.JoinSession.Peers, peer.Id)
				log.Infof("Peer %v disconnected", peer.Id)
			} else {
				peer.JoinQueue.RemovePeer(peer)
				log.Infof("Peer %v disconnected", peer.Id)
			}
			break
		}
		if cmd == 1 || peer.JoinSession == nil {
			log.Debug("continue with cmd value is 1 or joinSession is nil")
			continue
		}

		message, err := messages.ParseMessage(data)
		if err != nil {
			log.Errorf("Can not parse data from websocket: %v", err)
			break
		}

		// Check message type, forwarding the message data to corresponding channel.
		switch message.MsgType {
		case messages.C_KEY_EXCHANGE:
			keyex := &pb.KeyExchangeReq{}
			err := proto.Unmarshal(message.Data, keyex)
			if err != nil {
				log.Errorf("Can not unmarshal KeyExchangeReq: %v", err)
				break
			}
			keyex.PeerId = peer.Id
			peer.JoinSession.keyExchangeChan <- *keyex

		case messages.C_DC_EXP_VECTOR:
			dcExpVector := &pb.DcExpVector{}
			err := proto.Unmarshal(message.Data, dcExpVector)
			if err != nil {
				log.Errorf("Can not unmarshal DcExpVector: %v", err)
				break
			}
			dcExpVector.PeerId = peer.Id
			peer.JoinSession.dcExpVectorChan <- *dcExpVector

		case messages.C_DC_XOR_VECTOR:
			dcXorVector := &pb.DcXorVector{}
			err := proto.Unmarshal(message.Data, dcXorVector)
			if err != nil {
				log.Errorf("Can not unmarshal DcExpVector: %v", err)
				break
			}
			dcXorVector.PeerId = peer.Id
			peer.JoinSession.dcXorVectorChan <- *dcXorVector

		case messages.C_TX_INPUTS:
			txins := &pb.TxInputs{}
			err := proto.Unmarshal(message.Data, txins)
			if err != nil {
				log.Errorf("Can not unmarshal TxInputs: %v", err)
				break
			}
			txins.PeerId = peer.Id
			peer.JoinSession.txInputsChan <- *txins

		case messages.C_TX_SIGN:
			signTx := &pb.JoinTx{}
			err := proto.Unmarshal(message.Data, signTx)
			if err != nil {
				log.Errorf("Can not unmarshal JoinTx: %v", err)
				break
			}
			signTx.PeerId = peer.Id
			peer.JoinSession.txSignedTxChan <- *signTx

		case messages.C_TX_PUBLISH_RESULT:
			pubResult := &pb.PublishResult{}
			if peer.Id != peer.JoinSession.Publisher {
				log.Debugf("peer %d is not publisher %d", peer.Id, peer.JoinSession.Publisher)
				continue
			}
			err := proto.Unmarshal(message.Data, pubResult)
			if err != nil {
				log.Errorf("Can not unmarshal PublishResult: %v", err)
				break
			}
			pubResult.PeerId = peer.Id
			peer.JoinSession.txPublishResultChan <- *pubResult
		}

	}
}

// GenId generates random uint32.
func GenId() uint32 {
	id, err := rand.Int(rand.Reader, big.NewInt(4294967295))
	if err != nil {
		log.Criticalf("can not gen id %v", err)
		panic(err)
	}
	return uint32(id.Uint64())
}
