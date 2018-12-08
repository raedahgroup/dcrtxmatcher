package coinjoin

import (
	"bytes"
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
		Pk          []byte
		Vk          []byte
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
		Padding     field.Field
		TmpData     []byte

		TxIns       *wire.MsgTx
		SignedTx    *wire.MsgTx
		TicketPrice int64

		Close chan struct{}
	}

	// PeerReplayInfo contains necessary data for replay protocol.
	PeerReplayInfo struct {
		Id        uint32
		SessionId uint32
		Pk        []byte
		Vk        []byte
		SharedKey []byte

		// Peer number of pkscripts ~ number of tickets purchase
		NumMsg uint32
		// Peer slot index
		SlotIndex    []int
		DcExpVector  []field.Field
		DcXorVector  [][]byte
		DcExpPadding field.Field
		TmpData      []byte
		DcXorRng     []byte
	}

	PeerSlotInfo struct {
		Id uint32
		Pk []byte
		Vk []byte
		Sk []byte
		// Peer number of pkscripts ~ number of tickets purchase
		NumMsg uint32
		// Peer slot index
		SlotIndex     []int
		PkScriptsHash []field.Uint128
		PkScripts     [][]byte
	}

	JoinQueue struct {
		mu          sync.Mutex
		Peers       map[uint32]*PeerInfo
		NewPeerChan chan *PeerInfo
		Timeout     *time.Timer
		WillStart   bool
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
	peer.Pk = []byte{}
	peer.Vk = []byte{}
	//peer.SharedKey = []byte{}
	peer.TxIns = nil
	peer.SignedTx = nil
	peer.Commit = []byte{}
	peer.NumMsg = 0
	peer.Padding = field.Field{}
}

// Run does join transaction in every 2 minutes (setting in config file).
// If there is enough peers for join transaction, creates new join session.
func (diceMix *DiceMix) Run(joinQueue *JoinQueue) {
	startTime := false
	for {
		select {
		case peer := <-joinQueue.NewPeerChan:
			joinQueue.AddNewPeer(peer)

		case <-diceMix.sessionTicker.C:
			timeStartJoin := time.Now().Add(time.Second * time.Duration(diceMix.config.JoinTicker))
			queueSize := len(joinQueue.Peers)
			if queueSize == 0 {
				//log.Info("Zero participant connected")
				if startTime {
					log.Info("Will start next join session at", util.GetTimeString(timeStartJoin))
				}
				continue
			}
			if queueSize < diceMix.config.MinParticipants {
				log.Infof("Number participants %d, will wait for minimum %d", queueSize, diceMix.config.MinParticipants)
				if startTime {
					log.Info("Will start next join session at", util.GetTimeString(timeStartJoin))
				}
				continue
			}
			//log.Info("Will start next join session at", util.GetTimeString(timeStartJoin))
			startTime = true
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

// Run does join transaction in every 2 minutes (setting in config file).
// If there is enough peers for join transaction, creates new join session.
func (joinQueue *JoinQueue) Run(cfg Config) {
	time.Sleep(time.Second * time.Duration(cfg.JoinTicker))
	queueSize := len(joinQueue.Peers)
	if queueSize < cfg.MinParticipants {
		return
	}

	joinQueue.mu.Lock()
	sessionId := GenId()
	joinSession := NewJoinSession(sessionId, cfg.RoundTimeOut)
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
	joinQueue.WillStart = false
	joinQueue.mu.Unlock()

	// Run the join session
	joinSession.Config = &Config{RoundTimeOut: cfg.RoundTimeOut}
	go joinSession.run()
}

// NewJoinQueue creates new join queue.
func NewJoinQueue(joinTicker int) *JoinQueue {
	return &JoinQueue{
		Peers:       make(map[uint32]*PeerInfo),
		NewPeerChan: make(chan *PeerInfo),
	}
}

// NewDiceMix returns new dicemix struct with the given config.
func NewDiceMix(config *Config) *DiceMix {

	return &DiceMix{
		config:   config,
		Sessions: make(map[uint32]*JoinSession),
	}
}

// AddNewPeer adds new requested peer to join queue.
func (joinQueue *JoinQueue) AddNewPeer(peer *PeerInfo) {

	joinQueue.mu.Lock()
	defer joinQueue.mu.Unlock()

	log.Infof("New peer connected %d - %v", peer.Id, peer.IPAddr)
	joinQueue.Peers[peer.Id] = peer
	peer.JoinQueue = joinQueue
	peer.Close = make(chan struct{})

	log.Infof("Number waiting peers: %d", len(joinQueue.Peers))

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
	defer func() {
		peer.Conn.Close()
		close(peer.Close)
	}()

	for {
		select {
		case msg := <-peer.writeChan:
			//peer.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			err := peer.Conn.WriteMessage(websocket.BinaryMessage, msg)
			if err != nil {
				if peer.JoinSession != nil {
					if peer.JoinSession.State != StateCompleted {
						log.Infof("Peer %d disconnected at session state %s with error %v", peer.Id, peer.JoinSession.getStateString(), err)
					}
					peer.JoinSession.mu.Lock()
					switch peer.JoinSession.State {
					case StateKeyExchange:
						// Just remove, ignore and continue.
						peer.JoinSession.removePeer(peer.Id)
					case StateDcExponential, StateDcXor, StateTxInput, StateTxSign:
						// Consider malicious peer, remove and inform to others.
						ids := []uint32{peer.Id}
						peer.JoinSession.pushMaliciousInfo(ids)
						// Reset join session state.
						peer.JoinSession.State = StateKeyExchange
					case StateTxPublish:
						if peer.JoinSession.Publisher == peer.Id {
							peer.JoinSession.removePeer(peer.Id)
							if len(peer.JoinSession.Peers) <= 1 {
								// Terminates fail
							}
							// Select other peer to publish transaction.
							buffTx := bytes.NewBuffer(nil)
							buffTx.Grow(peer.JoinSession.JoinedTx.SerializeSize())
							err := peer.JoinSession.JoinedTx.BtcEncode(buffTx, 0)
							if err != nil {
								log.Errorf("Cannot execute BtcEncode: %v", err)
								peer.JoinSession.terminate()
								return
							}

							joinTx := &pb.JoinTx{}
							joinTx.Tx = buffTx.Bytes()
							joinTxData, err := proto.Marshal(joinTx)
							if err != nil {
								log.Errorf("Can not marshal signed transaction: %v", err)
								log.Infof("Session terminates fail")
								peer.JoinSession.terminate()
								return
							}
							joinTxMsg := messages.NewMessage(messages.S_TX_SIGN, joinTxData)
							pubId := peer.JoinSession.randomPublisher()
							peer.JoinSession.Publisher = pubId
							publisher := peer.JoinSession.Peers[pubId]
							log.Infof("Peer %d is randomly selected to publish tx %s", pubId, peer.JoinSession.JoinedTx.TxHash().String())
							publisher.writeChan <- joinTxMsg.ToBytes()
							peer.JoinSession.roundTimeout = time.NewTimer(time.Second * time.Duration(peer.JoinSession.Config.RoundTimeOut))
						}
					}
					peer.JoinSession.mu.Unlock()
				} else {
					peer.JoinQueue.RemovePeer(peer)
					log.Infof("Peer %v disconnected", peer.Id)
				}
				return
			}
		case <-peer.Close:
			return
		}
	}
}

// ReadMessages reads incoming data on peer's websocket and parses received data.
func (peer *PeerInfo) ReadMessages() {

	defer func() {
		peer.Conn.Close()
		peer.Close <- struct{}{}
	}()
	peer.Conn.SetReadLimit(maxMessageSize)

	for {
		cmd, data, err := peer.Conn.ReadMessage()
		if err != nil {
			if peer.JoinSession != nil {
				peer.JoinSession.mu.Lock()
				// Peer may disconnected, remove from join session.
				if peer.JoinSession.State != StateCompleted {
					log.Infof("Peer %d disconnected at session state %s with error %v", peer.Id, peer.JoinSession.getStateString(), err)
				} else {
					log.Infof("Peer %v disconnected", peer.Id)
				}

				switch peer.JoinSession.State {
				case StateKeyExchange:
					// Just remove, ignore and continue.
					peer.JoinSession.removePeer(peer.Id)
				case StateDcExponential, StateDcXor, StateTxInput, StateTxSign:
					// Consider malicious peer, remove and inform to others.
					if _, ok := peer.JoinSession.Peers[peer.Id]; ok {
						ids := []uint32{peer.Id}
						log.Debug("write error.StateDcExponential", ids)
						peer.JoinSession.pushMaliciousInfo(ids)
						// Reset join session state.
						peer.JoinSession.State = StateKeyExchange
					}
				case StateTxPublish:
					if _, ok := peer.JoinSession.Peers[peer.Id]; ok {
						if peer.JoinSession.Publisher == peer.Id {
							peer.JoinSession.removePeer(peer.Id)
							if len(peer.JoinSession.Peers) == 0 {
								// Terminates fail
								//log.Warnf("All peers disconnected at StatePublish. Session %d terminates fail.", peer.JoinSession.Id)
								peer.JoinSession.mu.Unlock()
								return
							}
							// Select other peer to publish transaction.
							buffTx := bytes.NewBuffer(nil)
							buffTx.Grow(peer.JoinSession.JoinedTx.SerializeSize())
							err := peer.JoinSession.JoinedTx.BtcEncode(buffTx, 0)
							if err != nil {
								log.Errorf("Cannot execute BtcEncode: %v", err)
								peer.JoinSession.terminate()
								peer.JoinSession.mu.Unlock()
								return
							}

							joinTx := &pb.JoinTx{}
							joinTx.Tx = buffTx.Bytes()
							joinTxData, err := proto.Marshal(joinTx)
							if err != nil {
								log.Errorf("Can not marshal signed transaction: %v", err)
								log.Infof("Session terminates fail")
								peer.JoinSession.terminate()
								peer.JoinSession.mu.Unlock()
								return
							}
							joinTxMsg := messages.NewMessage(messages.S_TX_SIGN, joinTxData)
							pubId := peer.JoinSession.randomPublisher()
							peer.JoinSession.Publisher = pubId
							publisher := peer.JoinSession.Peers[pubId]
							log.Infof("Peer %d is randomly selected to publish tx %s", pubId, peer.JoinSession.JoinedTx.TxHash().String())
							publisher.writeChan <- joinTxMsg.ToBytes()
							peer.JoinSession.roundTimeout = time.NewTimer(time.Second * time.Duration(peer.JoinSession.Config.RoundTimeOut))
						}
					}
				}
				peer.JoinSession.mu.Unlock()
			} else {
				peer.JoinQueue.RemovePeer(peer)
				log.Infof("Peer %d disconnected", peer.Id)
			}
			return
		}
		if cmd == 1 || peer.JoinSession == nil {
			log.Debug("continue with cmd value is 1 or joinSession is nil")
			continue
		}

		peer.JoinSession.mu.Lock()
		message, err := messages.ParseMessage(data)
		if err != nil {
			log.Errorf("Can not parse data from websocket: %v", err)
			continue
		}

		// Check message type, forwarding the message data to corresponding channel.
		switch message.MsgType {
		case messages.C_KEY_EXCHANGE:
			if peer.JoinSession.State != StateKeyExchange {
				// Peer sent data invalid state
				log.Infof("Current join session state is %s. Peer id %d has sent invalid state: StateKeyExchange",
					peer.JoinSession.getStateString(), peer.Id)
				continue
			}
			keyex := &pb.KeyExchangeReq{}
			err := proto.Unmarshal(message.Data, keyex)
			if err != nil {
				log.Errorf("Can not unmarshal KeyExchangeReq: %v", err)
				peer.JoinSession.removePeer(peer.Id)
				continue
			}
			keyex.PeerId = peer.Id
			peer.JoinSession.mu.Unlock()
			peer.JoinSession.keyExchangeChan <- *keyex
		case messages.C_DC_EXP_VECTOR:
			if peer.JoinSession.State != StateDcExponential {
				// Peer sent data invalid state
				log.Infof("Current join session state is %s. Peer id %d has sent invalid state: StateDcExponential",
					peer.JoinSession.getStateString(), peer.Id)
				continue
			}
			dcExpVector := &pb.DcExpVector{}
			err := proto.Unmarshal(message.Data, dcExpVector)
			if err != nil {
				log.Errorf("Can not unmarshal DcExpVector: %v", err)
				peer.JoinSession.pushMaliciousInfo([]uint32{peer.Id})
				continue
			}
			dcExpVector.PeerId = peer.Id
			peer.JoinSession.mu.Unlock()
			peer.JoinSession.dcExpVectorChan <- *dcExpVector
		case messages.C_DC_XOR_VECTOR:
			if peer.JoinSession.State != StateDcXor {
				// Peer sent data invalid state
				log.Infof("Current join session state is %s. Peer id %d has sent invalid state: StateDcXor",
					peer.JoinSession.getStateString(), peer.Id)
				continue
			}
			dcXorVector := &pb.DcXorVector{}
			err := proto.Unmarshal(message.Data, dcXorVector)
			if err != nil {
				log.Errorf("Can not unmarshal DcExpVector: %v", err)
				peer.JoinSession.pushMaliciousInfo([]uint32{peer.Id})
				continue
			}
			dcXorVector.PeerId = peer.Id
			peer.JoinSession.mu.Unlock()
			peer.JoinSession.dcXorVectorChan <- *dcXorVector
		case messages.C_TX_INPUTS:
			if peer.JoinSession.State != StateTxInput {
				// Peer sent data invalid state
				log.Infof("Current join session state is %s. Peer id %d has sent invalid state: StateTxInput",
					peer.JoinSession.getStateString(), peer.Id)
				continue
			}
			txins := &pb.TxInputs{}
			err := proto.Unmarshal(message.Data, txins)
			if err != nil {
				log.Errorf("Can not unmarshal TxInputs: %v", err)
				peer.JoinSession.pushMaliciousInfo([]uint32{peer.Id})
				continue
			}
			txins.PeerId = peer.Id
			peer.JoinSession.mu.Unlock()
			peer.JoinSession.txInputsChan <- *txins
		case messages.C_TX_SIGN:
			if peer.JoinSession.State != StateTxSign {
				// Peer sent data invalid state
				log.Infof("Current join session state is %s. Peer id %d has sent invalid state: StateTxSign",
					peer.JoinSession.getStateString(), peer.Id)
				continue
			}
			signTx := &pb.JoinTx{}
			err := proto.Unmarshal(message.Data, signTx)
			if err != nil {
				log.Errorf("Can not unmarshal JoinTx: %v", err)
				peer.JoinSession.pushMaliciousInfo([]uint32{peer.Id})
				continue
			}
			signTx.PeerId = peer.Id
			peer.JoinSession.mu.Unlock()
			peer.JoinSession.txSignedTxChan <- *signTx
		case messages.C_TX_PUBLISH_RESULT:
			if peer.JoinSession.State != StateTxPublish {
				// Peer sent data invalid state
				log.Infof("Current join session state is %s. Peer id %d has sent invalid state: StateTxPublish",
					peer.JoinSession.getStateString(), peer.Id)
				continue
			}
			pubResult := &pb.PublishResult{}
			pubResult.PeerId = peer.Id
			if peer.Id != peer.JoinSession.Publisher {
				log.Debugf("peer %d is not publisher %d", peer.Id, peer.JoinSession.Publisher)
				continue
			}
			err := proto.Unmarshal(message.Data, pubResult)
			if err != nil {
				log.Errorf("Can not unmarshal PublishResult: %v", err)
				peer.JoinSession.pushMaliciousInfo([]uint32{peer.Id})
				continue
			}
			peer.JoinSession.mu.Unlock()
			peer.JoinSession.txPublishResultChan <- *pubResult
		case messages.C_REVEAL_SECRET:
			workState := peer.JoinSession.State == StateDcExponential || peer.JoinSession.State == StateDcXor ||
				peer.JoinSession.State == StateTxInput || peer.JoinSession.State == StateRevealSecret
			if !workState {
				// Peer sent invalid data with state
				log.Infof("Current join session state is %s. Peer id %d has sent invalid state: StateRevealSecret",
					peer.JoinSession.getStateString(), peer.Id)
				continue
			}
			rs := &pb.RevealSecret{}
			err := proto.Unmarshal(message.Data, rs)
			if err != nil {
				log.Errorf("Can not unmarshal reveal secrect key: %v", err)
				peer.JoinSession.pushMaliciousInfo([]uint32{peer.Id})
				continue
			}
			rs.PeerId = peer.Id
			peer.JoinSession.mu.Unlock()
			peer.JoinSession.revealSecretChan <- *rs
		case messages.C_MESSAGE_NOT_FOUND:
			if !(peer.JoinSession.State == StateDcXor || peer.JoinSession.State == StateTxInput) {
				// Peer sent invalid data with state
				log.Infof("Current join session state is %s. Peer id %d has sent invalid state: StateMsgNotFound",
					peer.JoinSession.getStateString(), peer.Id)
				continue
			}
			if peer.JoinSession.maliciousFinding {
				continue
			}
			msg := &pb.MsgNotFound{}
			err := proto.Unmarshal(message.Data, msg)
			if err != nil {
				log.Errorf("Can not proto.Unmarshal: %v", err)
				peer.JoinSession.pushMaliciousInfo([]uint32{peer.Id})
				continue
			}

			msg.PeerId = peer.Id
			peer.JoinSession.maliciousFinding = true
			peer.JoinSession.mu.Unlock()
			peer.JoinSession.msgNotFoundChan <- *msg
		default:
			peer.JoinSession.mu.Unlock()
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
