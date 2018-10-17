package coinjoin

import (
	"crypto/rand"

	"math/big"
	"sync"
	"time"

	"github.com/decred/dcrd/wire"
	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"

	//	pb "github.com/raedahgroup/dcrtxmatcher/api/messages"
	//	"github.com/raedahgroup/dcrtxmatcher/finitefield"
	//	"github.com/raedahgroup/dcrtxmatcher/util"

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

		NumMsg uint32

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
		sync.Mutex
		WaitingPeers map[uint32]*PeerInfo
		NewPeerChan  chan *PeerInfo
	}

	DiceMix struct {
		sessionTicker *time.Ticker
		Sessions      map[uint32]*JoinSession
		config        *Config
	}

	// Config stores the parameters for the matcher engine
	Config struct {
		MinParticipants int
		RandomIndex     bool
		JoinTicker      int
		WaitingTimer    int
	}
)

func (diceMix *DiceMix) Run(joinQueue *JoinQueue) {

	for {

		select {

		case peer := <-joinQueue.NewPeerChan:
			joinQueue.AddNewPeer(peer)

		case <-diceMix.sessionTicker.C:
			//start join session
			timeStartJoin := time.Now().Add(time.Second * time.Duration(diceMix.config.JoinTicker))
			queueSize := len(joinQueue.WaitingPeers)
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

			joinQueue.Lock()
			sessionId := GenId()
			joinSession := NewJoinSession(sessionId)
			log.Infof("Start coin join transaction - sessionId %v", sessionId)

			for id, peer := range joinQueue.WaitingPeers {
				peer.SessionId = sessionId
				joinSession.Peers[id] = peer
				peer.JoinQueue = nil
				peer.JoinSession = joinSession

				coinJoinRes := &pb.CoinJoinRes{
					PeerId:    peer.Id,
					SessionId: peer.SessionId,
					//PeerIds:
				}

				data, err := proto.Marshal(coinJoinRes)
				if err != nil {
					log.Errorf("proto.Marshal coinJoinRes error: %v", err)
					break
				}

				message := messages.NewMessage(messages.S_JOIN_RESPONSE, data)

				peer.writeChan <- message.ToBytes()
			}

			//init new queue
			joinQueue.WaitingPeers = make(map[uint32]*PeerInfo)
			joinQueue.Unlock()

			//start join session
			joinSession.DiceMix = diceMix
			go joinSession.run()

		}
	}
}

func NewJoinQueue() *JoinQueue {
	return &JoinQueue{
		WaitingPeers: make(map[uint32]*PeerInfo),
		NewPeerChan:  make(chan *PeerInfo),
	}
}

func NewDiceMix(config *Config) *DiceMix {

	//log time will start join transaction
	timeStartJoin := time.Now().Add(time.Second * time.Duration(config.JoinTicker))
	log.Info("Will start join session at", util.GetTimeString(timeStartJoin))

	return &DiceMix{
		config:        config,
		Sessions:      make(map[uint32]*JoinSession),
		sessionTicker: time.NewTicker(time.Second * time.Duration(config.JoinTicker)),
	}
}

func (joinQueue *JoinQueue) AddNewPeer(peer *PeerInfo) {

	joinQueue.Lock()
	defer joinQueue.Unlock()

	log.Infof("New peer connected %v - %v", peer.Id, peer.IPAddr)

	joinQueue.WaitingPeers[peer.Id] = peer
	peer.JoinQueue = joinQueue

	log.Infof("Size of waiting peers %v", len(joinQueue.WaitingPeers))

	go peer.ReadMessages()
	go peer.WriteMessages()
}

func (joinQueue *JoinQueue) RemovePeer(peer *PeerInfo) {
	if joinQueue == nil {
		return
	}

	joinQueue.Lock()
	defer joinQueue.Unlock()
	log.Infof("Remove peer %v - %v from join queue", peer.Id, peer.IPAddr)
	delete(joinQueue.WaitingPeers, peer.Id)
}

func NewPeer(wsconn *websocket.Conn) *PeerInfo {
	return &PeerInfo{
		Id:        GenId(),
		Conn:      wsconn,
		writeChan: make(chan []byte),
	}
}

func (peer *PeerInfo) WriteMessages() {
	for {
		select {
		case msg := <-peer.writeChan:
			err := peer.Conn.WriteMessage(websocket.BinaryMessage, msg)
			if err != nil {
				log.Errorf("Write messsage to socket error: %v", err)
				if peer.JoinSession != nil {
					//remove from joinsesion
					delete(peer.JoinSession.Peers, peer.Id)
					log.Infof("Peer %v disconnected", peer.Id)
				} else {
					peer.JoinQueue.RemovePeer(peer)
					log.Infof("Peer %v disconnected", peer.Id)
				}
				break
			}
		}
	}
}

func (peer *PeerInfo) ReadMessages() {

	defer peer.Conn.Close()
	for {

		cmd, data, err := peer.Conn.ReadMessage()
		if err != nil {
			log.Errorf("Read messsage from socket error: %v", err)
			if peer.JoinSession != nil {
				//remove from joinsesion
				delete(peer.JoinSession.Peers, peer.Id)
				log.Infof("Peer %v disconnected", peer.Id)
			} else {
				peer.JoinQueue.RemovePeer(peer)
				log.Infof("Peer %v disconnected", peer.Id)
			}
			break
		}
		if cmd == 1 || peer.JoinSession == nil {
			log.Debug("continue with cmd == 1 and peer.JoinSession is nil")
			continue
		}

		message, err := messages.ParseMessage(data)
		if err != nil {
			log.Errorf("ParseMessage error: %v", err)
			break
		}

		switch message.MsgType {
		case messages.C_KEY_EXCHANGE:
			//log.Debug("C_KEY_EXCHANGE")
			keyex := &pb.KeyExchangeReq{}
			err := proto.Unmarshal(message.Data, keyex)
			if err != nil {
				log.Errorf("KeyExchangeReq Parsproto.Unmarshal error: %v", err)
				break
			}

			//log.Infof("key ex: %v", keyex)

			keyex.PeerId = peer.Id

			peer.JoinSession.keyExchangeChan <- *keyex
		case messages.C_DC_EXP_VECTOR:
			dcExpVector := &pb.DcExpVector{}
			//log.Debug("C_DC_EXP_VECTOR")
			err := proto.Unmarshal(message.Data, dcExpVector)
			if err != nil {
				log.Errorf("dcExpVector Parsproto.Unmarshal error: %v", err)
				break
			}
			//log.Debug("C_DC_EXP_VECTOR end")
			peer.JoinSession.dcExpVectorChan <- *dcExpVector

		case messages.C_DC_XOR_VECTOR:
			dcXorVector := &pb.DcXorVector{}

			err := proto.Unmarshal(message.Data, dcXorVector)
			if err != nil {
				log.Errorf("dcXorVector Parsproto.Unmarshal error: %v", err)
				break
			}
			//log.Debug("C_DC_XOR_VECTOR")
			peer.JoinSession.dcXorVectorChan <- *dcXorVector

		case messages.C_TX_INPUTS:
			txins := &pb.TxInputs{}
			//log.Debug("C_TX_INPUTS")

			err := proto.Unmarshal(message.Data, txins)
			if err != nil {
				log.Errorf("TxInputs Parsproto.Unmarshal error: %v", err)
				break
			}
			txins.PeerId = peer.Id
			peer.JoinSession.txInputsChan <- *txins

		case messages.C_TX_SIGN:
			signTx := &pb.JoinTx{}
			err := proto.Unmarshal(message.Data, signTx)
			if err != nil {
				log.Errorf("TxInputs Parsproto.Unmarshal error: %v", err)
				break
			}
			//log.Debug("C_TX_SIGN")
			signTx.PeerId = peer.Id
			peer.JoinSession.txSignedTxChan <- *signTx

		case messages.C_TX_PUBLISH_RESULT:
			//log.Debug("C_TX_PUBLISH_RESULT")
			if peer.Id != peer.JoinSession.Publisher {
				log.Debugf("peerId %d is not publisher %d", peer.Id, peer.JoinSession.Publisher)
				continue
			}
			peer.JoinSession.txPublishResultChan <- message.Data

		}

	}
}

func GenId() uint32 {
	id, err := rand.Int(rand.Reader, big.NewInt(4294967295))

	if err != nil {
		log.Criticalf("can not gen id %v", err)
		panic(err)
	}

	return uint32(id.Uint64())
}
