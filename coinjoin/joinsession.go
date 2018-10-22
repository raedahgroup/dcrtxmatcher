package coinjoin

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/decred/dcrd/wire"
	pb "github.com/decred/dcrwallet/dcrtxclient/api/messages"
	"github.com/decred/dcrwallet/dcrtxclient/finitefield"
	"github.com/decred/dcrwallet/dcrtxclient/messages"
	"github.com/decred/dcrwallet/dcrtxclient/util"
	"github.com/gogo/protobuf/proto"
	"github.com/raedahgroup/dcrtxmatcher/flint"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

type (
	JoinSession struct {
		Id uint32
		sync.Mutex
		Peers map[uint32]*PeerInfo
		//DiceMix *DiceMix
		Config *config
		State  int

		PKs      []*pb.PeerInfo
		JoinedTx *wire.MsgTx

		Publisher uint32

		keyExchangeChan     chan pb.KeyExchangeReq
		dcExpVectorChan     chan pb.DcExpVector
		dcXorVectorChan     chan pb.DcXorVector
		txInputsChan        chan pb.TxInputs
		txSignedTxChan      chan pb.JoinTx
		txPublishResultChan chan []byte
		roundTimeout        chan time.Timer
	}
)

func NewJoinSession(sessionId uint32) *JoinSession {
	return &JoinSession{
		Id:                  sessionId,
		Peers:               make(map[uint32]*PeerInfo),
		keyExchangeChan:     make(chan pb.KeyExchangeReq),
		dcExpVectorChan:     make(chan pb.DcExpVector),
		dcXorVectorChan:     make(chan pb.DcXorVector),
		txInputsChan:        make(chan pb.TxInputs),
		txSignedTxChan:      make(chan pb.JoinTx),
		PKs:                 make([]*pb.PeerInfo, 0),
		txPublishResultChan: make(chan []byte),
	}
}

func (joinSession *JoinSession) run() {
	var allMsgs [][]byte
	for {
		select {

		case req := <-joinSession.keyExchangeChan:
			joinSession.Lock()
			peer := joinSession.Peers[req.PeerId]

			if peer == nil {
				//send error to client
				log.Debug("joinSession.keyExchangeChan peer is nil")
				break
			}

			if len(peer.PK) == 0 {
				peer.PK = req.Pk
				peer.NumMsg = req.NumMsg
				joinSession.PKs = append(joinSession.PKs, &pb.PeerInfo{PeerId: peer.Id, Pk: peer.PK, NumMsg: req.NumMsg})

				log.Debug("Received key exchange request from peer", peer.Id)
			}

			//If there are enough public keys, broadcast to all peers
			if len(joinSession.Peers) == len(joinSession.PKs) {
				log.Debug("All peers have sent public key, broadcast all public keys to peers")
				keyex := &pb.KeyExchangeRes{
					Peers: joinSession.PKs,
				}

				data, err := proto.Marshal(keyex)
				if err != nil {
					log.Errorf("proto.Marshal keyexchange error: %v", err)
					break
				}

				message := messages.NewMessage(messages.S_KEY_EXCHANGE, data)
				for _, p := range joinSession.Peers {
					p.writeChan <- message.ToBytes()
				}

				//joinSession.roundTimeout = time.NewTimer(time.Second * )
			}
			joinSession.Unlock()
		case data := <-joinSession.dcExpVectorChan:
			joinSession.Lock()
			peerInfo := joinSession.Peers[data.PeerId]
			if peerInfo == nil {
				log.Debug("dcExpVector not include peerid")
				break
			}
			vector := make([]field.Field, 0)
			for i := 0; i < int(data.Len); i++ {
				b := data.Vector[i*16 : (i+1)*16]
				ff := field.NewFF(field.FromBytes(b))
				vector = append(vector, ff)
			}

			peerInfo.DcExpVector = vector
			peerInfo.Commit = data.Commit

			log.Debug("Received dc-net exponential from peer", peerInfo.Id)
			allSubmit := true
			for _, peer := range joinSession.Peers {
				if len(peer.DcExpVector) == 0 {
					allSubmit = false
				}
			}

			//If all peers sent dc-net exponential vector, we need combine (sum) with the same index of each peer
			//The sum of all peers will remove padding bytes that each peer has added.
			//And this time, we will having the real power sum of all peers
			if allSubmit {
				log.Debug("All peers sent dc-net exponential vector. Combine dc-net exponential from all peers to remove padding")
				polyDegree := len(vector)
				dcCombine := make([]field.Field, polyDegree)

				for _, peer := range joinSession.Peers {
					for i := 0; i < len(peer.DcExpVector); i++ {
						dcCombine[i] = dcCombine[i].Add(peer.DcExpVector[i])
					}
				}

				for _, ff := range dcCombine {
					log.Debug("Dc-combine:", ff.N.HexStr())
				}

				log.Debug("Will use flint to resolve polynomial to get roots as hash of pkscript")

				ret, roots := flint.GetRoots(field.Prime.HexStr(), dcCombine, polyDegree)
				log.Infof("Func returns: %d", ret)
				log.Infof("Number roots: %d", len(roots))
				log.Infof("Roots: %v", roots)

				//Will check whether the polynomial can solve or not
				if ret != 0 {
					//Some peers may sent incorrect dc-net expopential vector
					//Need to check and remove malcious peers
				}

				//Send to all peers the roots resolved
				allMsgHash := make([]byte, 0)
				for _, root := range roots {
					str := fmt.Sprintf("%032v", root)
					bytes, err := hex.DecodeString(str)
					if err != nil {
						log.Errorf("error DecodeString %v", err)
					}

					//remove zero message
					if len(bytes) == 16 {
						allMsgHash = append(allMsgHash, bytes...)
					}
				}

				msgdata := &pb.AllMessages{}
				msgdata.Len = uint32(len(roots))
				msgdata.Msgs = allMsgHash
				data, err := proto.Marshal(msgdata)
				if err != nil {
					log.Errorf("proto.Marshal keyexchange error: %v", err)
					break
				}

				msg := messages.NewMessage(messages.S_DC_EXP_VECTOR, data)
				for _, peer := range joinSession.Peers {
					peer.writeChan <- msg.ToBytes()
				}
			}
			joinSession.Unlock()

		case data := <-joinSession.dcXorVectorChan:
			joinSession.Lock()
			dcXor := make([][]byte, 0)
			for i := 0; i < int(data.Len); i++ {
				msg := data.Vector[i*messages.PkScriptSize : (i+1)*messages.PkScriptSize]
				dcXor = append(dcXor, msg)
			}

			peerInfo := joinSession.Peers[data.PeerId]
			if peerInfo == nil {
				log.Debug("dcExpVector not include peerid")
			}
			peerInfo.DcXorVector = dcXor
			log.Debug("Received dc-net xor vector from peer", peerInfo.Id)

			allSubmit := true
			for _, peer := range joinSession.Peers {
				if len(peer.DcXorVector) == 0 {
					allSubmit = false
					break
				}
			}

			//If all peers have sent dc-net xor vector, will solve xor vector to get all peers's pkscripts
			allMsgs = make([][]byte, len(peerInfo.DcXorVector))
			var err error = nil
			if allSubmit {
				log.Debug("Combine xor vector to remove padding xor and get all pkscripts hash")
				//Base on equation: (Pkscript ^ P ^ P1 ^ P2...) ^ (P ^ P1 ^ P2...) = Pkscript
				//Each peer will send Pkscript ^ P ^ P1 ^ P2... bytes to server
				//Server combine (xor) all dc-net xor vectors and will have Pkscript ^ P ^ P1 ^ P2... ^ (P ^ P1 ^ P2...) = Pkscript
				//But server could not know which Pkscript belongs to any peer because only peer know it's slot index
				//And each peer only knows it's Pkscript itself
				for i := 0; i < len(peerInfo.DcXorVector); i++ {
					for _, peer := range joinSession.Peers {
						allMsgs[i], err = util.XorBytes(allMsgs[i], peer.DcXorVector[i])
						if err != nil {
							log.Errorf("error XorBytes %v", err)
						}
					}
				}
			}
			if allSubmit {
				for _, msg := range allMsgs {
					log.Debugf("Pkscript %x", msg)
				}

				//Signal to all peers that server has got all pkscripts
				//Peers will process next step
				dcXorRet := &pb.DcXorVectorResult{}
				dcXorData, err := proto.Marshal(dcXorRet)
				if err != nil {
					log.Errorf("error Marshal DcXorVectorResult %v", err)
				}
				message := messages.NewMessage(messages.S_DC_XOR_VECTOR, dcXorData)
				for _, peer := range joinSession.Peers {
					peer.writeChan <- message.ToBytes()
				}
				log.Debug("Has solved dc-net xor vector and got all pkscripts")
			}
			joinSession.Unlock()

		case txins := <-joinSession.txInputsChan:
			joinSession.Lock()
			peer := joinSession.Peers[txins.PeerId]
			//Server will use the ticket price that sent by each peer to construct the join transaction
			peer.TicketPrice = txins.TicketPrice

			var tx wire.MsgTx
			buf := bytes.NewReader(txins.Txins)
			err := tx.BtcDecode(buf, 0)
			if err != nil {
				log.Errorf("error BtcDecode %v", err)
				break
			}
			peer.TxIns = &tx

			log.Debugf("Received txin from peer %d, number txin :%d, number txout :%d", peer.Id, len(tx.TxIn), len(tx.TxOut))
			allSubmit := true
			for _, peer := range joinSession.Peers {
				if peer.TxIns == nil {
					allSubmit = false
					break
				}
			}

			//With pkscripts solved from dc-net xor vector, we will build the transaction
			//Each pkscript will be one txout with amout is ticket price and fee
			//Combine with transaction input from peer, we can build unsigned transaction
			var joinedtx *wire.MsgTx
			if allSubmit {
				log.Debug("All peers sent txin, will create join tx for signing")
				for _, peer := range joinSession.Peers {
					if joinedtx == nil {
						joinedtx = peer.TxIns
						for i := range peer.TxIns.TxIn {
							peer.InputIndex = append(peer.InputIndex, i)
						}
					} else {
						endIndex := len(joinedtx.TxIn)
						joinedtx.TxIn = append(joinedtx.TxIn, peer.TxIns.TxIn...)
						joinedtx.TxOut = append(joinedtx.TxOut, peer.TxIns.TxOut...)

						for i := range peer.TxIns.TxIn {
							peer.InputIndex = append(peer.InputIndex, i+endIndex)
						}
					}
				}
				for _, msg := range allMsgs {
					txout := wire.NewTxOut(peer.TicketPrice, msg)
					joinedtx.AddTxOut(txout)
				}

				//Send unsign join transaction to peers
				buffTx := bytes.NewBuffer(nil)
				buffTx.Grow(joinedtx.SerializeSize())
				err := joinedtx.BtcEncode(buffTx, 0)
				if err != nil {
					log.Errorf("error BtcEncode %v", err)
					break
				}

				joinTx := &pb.JoinTx{}
				joinTx.Tx = buffTx.Bytes()
				joinTxData, err := proto.Marshal(joinTx)
				if err != nil {

				}
				joinTxMsg := messages.NewMessage(messages.S_JOINED_TX, joinTxData)
				for _, peer := range joinSession.Peers {
					peer.writeChan <- joinTxMsg.ToBytes()
				}
				log.Debug("Broadcast joined tx to all peers")
			}
			joinSession.Unlock()
		case signedTx := <-joinSession.txSignedTxChan:
			//Each peer after received unsigned join transaction then sign their own transaction input
			//and send to server
			joinSession.Lock()
			peer := joinSession.Peers[signedTx.PeerId]

			var tx wire.MsgTx
			reader := bytes.NewReader(signedTx.Tx)
			err := tx.BtcDecode(reader, 0)
			if err != nil {

			}
			if peer.SignedTx != nil {
				continue
			}
			peer.SignedTx = &tx
			log.Debug("Received signed tx from peer", peer.Id)

			//Join signed transaction from each peer to one transaction
			if joinSession.JoinedTx == nil {
				joinSession.JoinedTx = tx.Copy()
			} else {
				for _, index := range peer.InputIndex {
					joinSession.JoinedTx.TxIn[index] = tx.TxIn[index]
				}
			}

			allSubmit := true
			for _, peer := range joinSession.Peers {
				if peer.SignedTx == nil {
					allSubmit = false
					break
				}
			}
			if allSubmit {
				//Send the joined transaction to all peer in join session
				//Random select peer to publish transaction.
				//TODO: publish transaction from server
				log.Info("Merged signed tx from all peers")
				buffTx := bytes.NewBuffer(nil)
				buffTx.Grow(joinSession.JoinedTx.SerializeSize())

				err := joinSession.JoinedTx.BtcEncode(buffTx, 0)
				if err != nil {
					log.Errorf("error BtcEncode %v", err)
					break
				}

				joinTx := &pb.JoinTx{}
				joinTx.Tx = buffTx.Bytes()
				joinTxData, err := proto.Marshal(joinTx)
				if err != nil {

				}
				publisher := rand.Intn(len(joinSession.Peers))
				joinTxMsg := messages.NewMessage(messages.S_TX_SIGN, joinTxData)
				n := 0
				for _, peer := range joinSession.Peers {
					if n == publisher {
						peer.Publisher = true
						log.Infof("Peer %d is random selected to publish tx", peer.Id)
						joinSession.Publisher = peer.Id
						peer.writeChan <- joinTxMsg.ToBytes()
						break
					}
					n++
				}

			}
			joinSession.Unlock()
		case pubResult := <-joinSession.txPublishResultChan:
			//Random peer has published transaction, send back to other peers for purchase ticket
			joinSession.Lock()
			msg := messages.NewMessage(messages.S_TX_PUBLISH_RESULT, pubResult)
			for _, peer := range joinSession.Peers {
				peer.writeChan <- msg.ToBytes()
			}
			joinSession.Unlock()
			log.Info("Broadcast published tx to all peers")
		}
	}
}
