package coinjoin

import (
	"bytes"
	"crypto/elliptic"
	"encoding/hex"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/decred/dcrd/wire"
	pb "github.com/decred/dcrwallet/dcrtxclient/api/messages"
	"github.com/decred/dcrwallet/dcrtxclient/chacharng"
	"github.com/decred/dcrwallet/dcrtxclient/finitefield"
	"github.com/decred/dcrwallet/dcrtxclient/messages"
	"github.com/decred/dcrwallet/dcrtxclient/ripemd128"
	"github.com/decred/dcrwallet/dcrtxclient/util"
	"github.com/gogo/protobuf/proto"
	"github.com/huyntsgs/go-ecdh"
	"github.com/raedahgroup/dcrtxmatcher/flint"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

const (
	StateKeyExchange   = 1
	StateDcExponential = 2
	StateDcXor         = 3
	StateTxInput       = 4
	StateTxSign        = 5
	StateTxPublish     = 6
	StateCompleted     = 7
	StateRevealSecret  = 8
)

type (
	JoinSession struct {
		Id       uint32
		mu       sync.Mutex
		Peers    map[uint32]*PeerInfo
		Config   *Config
		State    int
		TotalMsg int

		PeersMsgInfo []*pb.PeerInfo
		JoinedTx     *wire.MsgTx
		Publisher    uint32

		findMalicious bool

		keyExchangeChan     chan pb.KeyExchangeReq
		dcExpVectorChan     chan pb.DcExpVector
		dcXorVectorChan     chan pb.DcXorVector
		txInputsChan        chan pb.TxInputs
		txSignedTxChan      chan pb.JoinTx
		txPublishResultChan chan pb.PublishResult
		revealSecretChan    chan pb.RevealSecret
		msgNotFoundChan     chan pb.MsgNotFound
		roundTimeout        *time.Timer
	}
)

// NewJoinSession returns new join session with parameters session id and roundTimeOut.
func NewJoinSession(sessionId uint32, roundTimeOut int) *JoinSession {
	return &JoinSession{
		Id:                  sessionId,
		Peers:               make(map[uint32]*PeerInfo),
		keyExchangeChan:     make(chan pb.KeyExchangeReq),
		dcExpVectorChan:     make(chan pb.DcExpVector),
		dcXorVectorChan:     make(chan pb.DcXorVector),
		txInputsChan:        make(chan pb.TxInputs),
		txSignedTxChan:      make(chan pb.JoinTx),
		revealSecretChan:    make(chan pb.RevealSecret),
		PeersMsgInfo:        make([]*pb.PeerInfo, 0),
		txPublishResultChan: make(chan pb.PublishResult),
		msgNotFoundChan:     make(chan pb.MsgNotFound),
		roundTimeout:        time.NewTimer(time.Second * time.Duration(roundTimeOut)),
		State:               StateKeyExchange,
	}
}

// removePeer removes peer from join session and disconnected websocket connection.
func (joinSession *JoinSession) removePeer(peerId uint32) {
	peer := joinSession.Peers[peerId]
	delete(joinSession.Peers, peerId)
	if peer != nil {
		peer.Conn.Close()
	}
	log.Infof("Remove peer %d from join session", peerId)
}

// pushMaliciousInfo removes peer ids from join session and disconnects.
// Also generates new session id, new peer id for each remaining peer
// to start the join session from beginning.
func (joinSession *JoinSession) pushMaliciousInfo(missedPeers []uint32) {

	joinSession.mu.Lock()
	defer joinSession.mu.Unlock()
	log.Debug("Len of malicious", len(missedPeers))
	malicious := &pb.MaliciousPeers{}
	for _, Id := range missedPeers {
		joinSession.removePeer(Id)
	}
	malicious.PeerIds = missedPeers

	if len(joinSession.Peers) <= 0 {
		log.Info("All peers not sending data in time, join session terminates")
		// Send terminate fails to client.
		return
	}

	// Re-generate the session id and peer id
	malicious.SessionId = GenId()
	newPeers := make(map[uint32]*PeerInfo, 0)
	log.Debug("Remaining peers in join session: ", joinSession.Peers)
	if len(joinSession.Peers) > 0 {
		for _, peer := range joinSession.Peers {
			malicious.PeerId = GenId()
			// Update new id generated.
			peer.ResetData(malicious.PeerId, malicious.SessionId)
			data, _ := proto.Marshal(malicious)
			peer.TmpData = data
			newPeers[peer.Id] = peer
		}
	}
	joinSession.Peers = newPeers
	joinSession.JoinedTx = nil
	joinSession.PeersMsgInfo = []*pb.PeerInfo{}
	joinSession.Id = malicious.SessionId
	joinSession.State = StateKeyExchange
	joinSession.TotalMsg = 0

	// After all change updated, inform clients for malicious information.
	log.Debug("len of joinSession.Peers to push malicious ", len(joinSession.Peers))
	for _, peer := range joinSession.Peers {
		log.Debug("Sent S_MALICIOUS_PEERS")
		msg := messages.NewMessage(messages.S_MALICIOUS_PEERS, peer.TmpData).ToBytes()
		peer.writeChan <- msg
	}

	log.Debug("Remaining peers in join session after updated: ", joinSession.Peers)
}

// run checks for join session's incoming data.
// Each peer sends data, server receives and processes data then
// sends back peers the information for next round.
func (joinSession *JoinSession) run() {
	var allPkScripts [][]byte
	missedPeers := make([]uint32, 0)

	// Stop round timer
	defer joinSession.roundTimeout.Stop()

	// We use label to break for loop when join session completed.
LOOP:
	for {
		select {
		case <-joinSession.roundTimeout.C:
			log.Info("Timeout.")
			// We use timer to control whether peers send data in time.
			// With one process of join session, server waits maximum time
			// for client process is 30 seconds (setting in config file).
			// After that time, client still not send data,
			// server will consider the client is malicious and terminate.
			for _, peer := range joinSession.Peers {
				switch joinSession.State {
				case StateKeyExchange:
					// With state key exchange, we do not need to inform other peers,
					// just remove and ignore this peer.
					if len(peer.Pk) == 0 {
						joinSession.removePeer(peer.Id)
						log.Infof("Peer id %v did not send key exchange data in time", peer.Id)
					}
				case StateDcExponential:
					// From this state, when one peer is malicious and terminated,
					// the join session has to restarted from the beginning.
					if len(peer.DcExpVector) == 0 {
						missedPeers = append(missedPeers, peer.Id)
						log.Infof("Peer id %v did not send dc-net exponential data in time", peer.Id)
					}
				case StateDcXor:
					if len(peer.DcExpVector) == 0 {
						missedPeers = append(missedPeers, peer.Id)
						log.Infof("Peer id %v did not send dc-net xor data in time", peer.Id)
					}
				case StateTxInput:
					if peer.TxIns == nil {
						missedPeers = append(missedPeers, peer.Id)
						log.Infof("Peer id %v did not send transaction input data in time", peer.Id)
					}
				case StateTxSign:
					if peer.SignedTx == nil {
						missedPeers = append(missedPeers, peer.Id)
						log.Infof("Peer id %v did not send signed transaction data in time", peer.Id)
					}
				case StateTxPublish:
					joinSession.mu.Lock()
					if joinSession.Publisher == peer.Id {
						log.Infof("Peer id %v did not send the published transaction data in time", peer.Id)
						joinSession.removePeer(peer.Id)

						if len(joinSession.Peers) <= 1 {
							// Protocol terminates fail

						}
						// Select other peer to publish transaction
						pubId := joinSession.randomPublisher()
						joinSession.Publisher = pubId
						joinTxMsg := messages.NewMessage(messages.S_TX_SIGN, []byte{0x00})
						peer.writeChan <- joinTxMsg.ToBytes()
						joinSession.roundTimeout = time.NewTimer(time.Second * time.Duration(joinSession.Config.RoundTimeOut))
					}
					joinSession.mu.Unlock()
				case StateRevealSecret:
					if len(peer.Vk) == 0 {
						missedPeers = append(missedPeers, peer.Id)
						log.Infof("Peer id %d did not send secret key in time", peer.Id)
					}
				}
			}

			// Inform to remaining peers in join session.
			if len(missedPeers) > 0 {
				joinSession.pushMaliciousInfo(missedPeers)
				// Reset join session state.
				joinSession.State = StateKeyExchange
			}
		case keyExchange := <-joinSession.keyExchangeChan:
			joinSession.mu.Lock()
			peer := joinSession.Peers[keyExchange.PeerId]
			if peer == nil {
				log.Errorf("Can not find join session with peer id %d", keyExchange.PeerId)
				joinSession.mu.Unlock()
				continue
			}

			// Validate public key and ignore peer if public key is not valid.
			ecp256 := ecdh.NewEllipticECDH(elliptic.P256())
			_, valid := ecp256.Unmarshal(keyExchange.Pk)
			if !valid {
				// Public key is invalid
				joinSession.removePeer(keyExchange.PeerId)
				joinSession.mu.Unlock()
				continue
			}

			peer.Pk = keyExchange.Pk
			peer.NumMsg = keyExchange.NumMsg
			joinSession.PeersMsgInfo = append(joinSession.PeersMsgInfo, &pb.PeerInfo{PeerId: peer.Id, Pk: peer.Pk, NumMsg: keyExchange.NumMsg})

			log.Debug("Received key exchange request from peer", peer.Id)

			// Broadcast to all peers when there are enough public keys.
			if len(joinSession.Peers) == len(joinSession.PeersMsgInfo) {
				log.Debug("All peers have sent public key, broadcast all public keys to peers")
				keyex := &pb.KeyExchangeRes{
					Peers: joinSession.PeersMsgInfo,
				}

				data, err := proto.Marshal(keyex)
				if err != nil {
					log.Errorf("Can not marshal keyexchange: %v", err)
					break
				}

				message := messages.NewMessage(messages.S_KEY_EXCHANGE, data)
				for _, p := range joinSession.Peers {
					p.writeChan <- message.ToBytes()
				}
				joinSession.roundTimeout = time.NewTimer(time.Second * time.Duration(joinSession.Config.RoundTimeOut))
				joinSession.State = StateDcExponential
			}
			joinSession.mu.Unlock()
		case data := <-joinSession.dcExpVectorChan:
			joinSession.mu.Lock()
			peerInfo := joinSession.Peers[data.PeerId]
			if peerInfo == nil {
				log.Debug("joinSession does not include peerid", data.PeerId)
				joinSession.mu.Unlock()
				continue
			}
			vector := make([]field.Field, 0)
			for i := 0; i < int(data.Len); i++ {
				b := data.Vector[i*messages.PkScriptHashSize : (i+1)*messages.PkScriptHashSize]
				ff := field.NewFF(field.FromBytes(b))
				vector = append(vector, ff)
				log.Debugf("Received dc-net exp vector %d - %x", peerInfo.Id, b)
			}

			peerInfo.DcExpVector = vector
			//peerInfo.Commit = data.Commit

			log.Debug("Received dc-net exponential from peer", peerInfo.Id)
			allSubmit := true
			for _, peer := range joinSession.Peers {
				if len(peer.DcExpVector) == 0 {
					allSubmit = false
				}
			}

			// If all peers sent dc-net exponential vector, we need combine (sum) with the same index of each peer.
			// The sum of all peers will remove padding bytes that each peer has added.
			// And this time, we will having the real power sum of all peers.
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

				// Check whether the polynomial could be solved or not
				if ret != 0 {
					// Some peers may sent incorrect dc-net expopential vector.
					// Peers need to reveal their secrect key.
					msg := messages.NewMessage(messages.S_REVEAL_SECRET, []byte{0x00})
					for _, peer := range joinSession.Peers {
						peer.writeChan <- msg.ToBytes()
					}
					joinSession.mu.Unlock()
					continue
				}

				// Send to all peers the roots resolved
				allMsgHash := make([]byte, 0)
				for _, root := range roots {
					str := fmt.Sprintf("%032v", root)
					bytes, _ := hex.DecodeString(str)

					// Only get correct message size
					if len(bytes) == messages.PkScriptHashSize {
						allMsgHash = append(allMsgHash, bytes...)
					} else {
						log.Warnf("Got pkscript hash from flint with size %d - %x", len(bytes), bytes)
					}
				}

				msgData := &pb.AllMessages{}
				msgData.Len = uint32(len(roots))
				msgData.Msgs = allMsgHash
				data, err := proto.Marshal(msgData)
				if err != nil {
					log.Errorf("Can not marshal all messages data: %v", err)
					break
				}

				msg := messages.NewMessage(messages.S_DC_EXP_VECTOR, data)
				for _, peer := range joinSession.Peers {
					peer.writeChan <- msg.ToBytes()
				}
				joinSession.roundTimeout = time.NewTimer(time.Second * time.Duration(joinSession.Config.RoundTimeOut))
				joinSession.State = StateDcXor
			}
			joinSession.mu.Unlock()
			//			log.Debug("Sleep 15 sec for stop one client")
			//			time.Sleep(time.Duration(6 * time.Second))
		case data := <-joinSession.dcXorVectorChan:
			joinSession.mu.Lock()
			dcXor := make([][]byte, 0)
			for i := 0; i < int(data.Len); i++ {
				msg := data.Vector[i*messages.PkScriptSize : (i+1)*messages.PkScriptSize]
				dcXor = append(dcXor, msg)
			}

			peer := joinSession.Peers[data.PeerId]
			if peer == nil {
				log.Debug("joinSession %d does not include peer %d", joinSession.Id, data.PeerId)
				joinSession.mu.Unlock()
				continue
			}
			peer.DcXorVector = dcXor
			log.Debug("Received dc-net xor vector from peer", peer.Id)

			allSubmit := true
			for _, peer := range joinSession.Peers {
				if len(peer.DcXorVector) == 0 {
					allSubmit = false
					break
				}
			}

			// If all peers have sent dc-net xor vector, will solve xor vector to get all peers's pkscripts
			allPkScripts = make([][]byte, len(peer.DcXorVector))
			var err error = nil
			if allSubmit {
				log.Debug("Combine xor vector to remove padding xor and get all pkscripts hash")
				// Base on equation: (pkscript ^ P ^ P1 ^ P2...) ^ (P ^ P1 ^ P2...) = pkscript.
				// Each peer will send pkscript ^ P ^ P1 ^ P2... bytes to server.
				// Server combine (xor) all dc-net xor vectors and will have pkscript ^ P ^ P1 ^ P2... ^ (P ^ P1 ^ P2...) = pkscript.
				// But server could not know which pkscript belongs to any peer because only peer know it's slot index.
				// And each peer only knows it's pkscript itself.
				for i := 0; i < len(peer.DcXorVector); i++ {
					for _, peer := range joinSession.Peers {
						allPkScripts[i], err = util.XorBytes(allPkScripts[i], peer.DcXorVector[i])
						if err != nil {
							log.Errorf("error XorBytes %v", err)
						}
					}
				}
			}
			if allSubmit {
				// Signal to all peers that server has got all pkscripts.
				// Peers will process next step
				dcXorRet := &pb.DcXorVectorResult{}
				msgs := make([]byte, 0)
				for _, msg := range allPkScripts {
					//log.Debugf("Pkscript %x, len msg %d", msg, len(msg))
					msgs = append(msgs, msg...)
				}
				dcXorRet.Msgs = msgs
				//log.Debugf("Len of dcXorRet.Msgs %d, len allPkScripts %d", len(dcXorRet.Msgs), len(allPkScripts))

				dcXorData, err := proto.Marshal(dcXorRet)
				if err != nil {
					log.Errorf("Can not marshal DcXorVectorResult: %v", err)
				}

				log.Debug("Has solved dc-net xor vector and got all pkscripts")
				joinSession.roundTimeout = time.NewTimer(time.Second * time.Duration(joinSession.Config.RoundTimeOut))
				joinSession.State = StateTxInput
				message := messages.NewMessage(messages.S_DC_XOR_VECTOR, dcXorData)
				for _, peer := range joinSession.Peers {
					peer.writeChan <- message.ToBytes()
				}
			}
			joinSession.mu.Unlock()
		case txins := <-joinSession.txInputsChan:
			joinSession.mu.Lock()
			peer := joinSession.Peers[txins.PeerId]
			if peer == nil {
				log.Debug("joinSession %d does not include peer %d", joinSession.Id, txins.PeerId)
				joinSession.mu.Unlock()
				continue
			}
			// Server will use the ticket price that sent by each peer to construct the join transaction.
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

			// With pkscripts solved from dc-net xor vector, we will build the transaction.
			// Each pkscript will be one txout with amout is ticket price + fee.
			// Combine with transaction input from peer, we can build unsigned transaction.
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
				for _, msg := range allPkScripts {
					txout := wire.NewTxOut(peer.TicketPrice, msg)
					joinedtx.AddTxOut(txout)
				}

				// Send unsign join transaction to peers
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
				joinSession.roundTimeout = time.NewTimer(time.Second * time.Duration(joinSession.Config.RoundTimeOut))
				joinSession.State = StateTxSign
			}
			joinSession.mu.Unlock()
		case signedTx := <-joinSession.txSignedTxChan:
			// Each peer after received unsigned join transaction then sign their own transaction input
			// and send to server
			joinSession.mu.Lock()
			peer := joinSession.Peers[signedTx.PeerId]
			if peer == nil {
				log.Debug("joinSession %d does not include peer %d", joinSession.Id, signedTx.PeerId)
				joinSession.mu.Unlock()
				continue
			}

			var tx wire.MsgTx
			reader := bytes.NewReader(signedTx.Tx)
			err := tx.BtcDecode(reader, 0)
			if err != nil {

			}
			if peer.SignedTx != nil {
				joinSession.mu.Unlock()
				continue
			}
			peer.SignedTx = &tx
			log.Debug("Received signed tx from peer", peer.Id)

			// Join signed transaction from each peer to one transaction.
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
				// Send the joined transaction to all peer in join session.
				// Random select peer to publish transaction.
				// TODO: publish transaction from server
				log.Info("Merged signed tx from all peers")
				buffTx := bytes.NewBuffer(nil)
				buffTx.Grow(joinSession.JoinedTx.SerializeSize())

				err := joinSession.JoinedTx.BtcEncode(buffTx, 0)
				if err != nil {
					log.Errorf("Cannot execute BtcEncode: %v", err)
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
				joinSession.roundTimeout = time.NewTimer(time.Second * time.Duration(joinSession.Config.RoundTimeOut/3))
				joinSession.State = StateTxPublish
			}
			joinSession.mu.Unlock()
		case pubResult := <-joinSession.txPublishResultChan:
			// Random peer has published transaction, send back to other peers for purchase ticket
			joinSession.mu.Lock()
			msg := messages.NewMessage(messages.S_TX_PUBLISH_RESULT, pubResult.Tx)
			for _, peer := range joinSession.Peers {
				peer.writeChan <- msg.ToBytes()
			}
			joinSession.State = StateCompleted
			joinSession.mu.Unlock()
			log.Info("Broadcast published tx to all peers")
			// Need to break for loop to terminate the join session
			break LOOP
		case revealSecret := <-joinSession.revealSecretChan:
			// Save verify key
			joinSession.mu.Lock()
			peerInfo := joinSession.Peers[revealSecret.PeerId]
			if peerInfo == nil {
				joinSession.mu.Unlock()
				log.Debug("joinSession %d does not include peer %d", joinSession.Id, revealSecret.PeerId)
				continue
			}

			// TODO: Verify pk and vk match.
			peerInfo.Vk = revealSecret.Vk
			log.Debugf("Peer %d submit verify key %x", peerInfo.Id, peerInfo.Vk)

			allSubmit := true
			for _, peer := range joinSession.Peers {
				if len(peer.Vk) == 0 {
					allSubmit = false
				}
			}
			maliciousIds := make([]uint32, 0)
			if allSubmit {
				// Replay all message protocol to find the malicious peers.
				// Create peer's random bytes for dc-net exponential and dc-net xor.
				ecp256 := ecdh.NewEllipticECDH(elliptic.P256())
				replayPeers := make(map[uint32]map[uint32]*PeerReplayInfo, 0)

				for _, p := range joinSession.Peers {
					replayPeer := make(map[uint32]*PeerReplayInfo)
					pVk := ecp256.UnmarshalPrivateKey(p.Vk)
					for _, op := range joinSession.Peers {
						if p.Id == op.Id {
							continue
						}
						opeerInfo := &PeerReplayInfo{}
						opPk, _ := ecp256.Unmarshal(op.Pk)

						// Generate shared key with other peer from peer's private key and other peer's public key
						sharedKey, err := ecp256.GenSharedSecret32(pVk, opPk)
						if err != nil {
							log.Errorf("Can not generate shared secret key: %v", err)
							//return nil, err
						}

						opeerInfo.Sk = sharedKey
						opeerInfo.Id = op.Id
						replayPeer[op.Id] = opeerInfo
					}
					replayPeers[p.Id] = replayPeer
					joinSession.TotalMsg += int(p.NumMsg)
				}

				// Maintain counter of the number incorrect share key of each peer.
				// If one peer with more than one share key is incorrect then
				// it is true the peer is malicious.
				compareCount := make(map[uint32]int)
				for pid, replayPeer := range replayPeers {
					for opid, opReplayPeer := range replayPeers {
						if pid == opid {
							continue
						}

						pInfo := opReplayPeer[pid]
						opInfo := replayPeer[opid]
						if bytes.Compare(pInfo.Sk, opInfo.Sk) != 0 {
							log.Debugf("compare vk %x of peer %d with vk %x of peer %d", pInfo.Sk, pInfo.Id, opInfo.Sk, opInfo.Id)
							// Increase compare counter
							if _, ok := compareCount[pid]; ok {
								compareCount[pid] = compareCount[pid] + 1
							} else {
								compareCount[pid] = 1
							}

							if _, ok := compareCount[opid]; ok {
								compareCount[opid] = compareCount[opid] + 1
							} else {
								compareCount[opid] = 1
							}
						}
					}
				}

				for pid, count := range compareCount {
					log.Debug("Peer %d, compare counter %d", pid, count)
					// Total number checking of share key is wrong
					// means this peer submit invalid pk/vk key pair.
					if count >= len(joinSession.Peers)-1 {
						// Peer is malicious
						maliciousIds = append(maliciousIds, pid)
						log.Infof("Peer %d is malicious - sent invalid public/verify key pair", pid)
					}
				}

				// Share keys are correct. Check dc-net exponential and dc-net xor vector.
				peerSlotInfos := make(map[uint32]*PeerSlotInfo, 0)
				for pid, pInfo := range joinSession.Peers {
					replayPeer := replayPeers[pid]

					expVector := make([]field.Field, len(pInfo.DcExpVector))
					copy(expVector, pInfo.DcExpVector)
					slotInfo := &PeerSlotInfo{Id: pid}
					for opid, opInfo := range replayPeer {
						dcexpRng, err := chacharng.RandBytes(opInfo.Sk, messages.ExpRandSize)
						if err != nil {
							//return nil, errors.E(op, err)
						}
						dcexpRng = append([]byte{0, 0, 0, 0}, dcexpRng...)

						// For random byte of Xor vector, we get the same size of pkscript is 25 bytes
						dcXorRng, err := chacharng.RandBytes(opInfo.Sk, messages.PkScriptSize)
						if err != nil {
							//return nil, err
						}

						padding := field.NewFF(field.FromBytes(dcexpRng))
						for i := 0; i < int(pInfo.NumMsg); i++ {
							if pid > opid {
								expVector[i] = expVector[i].Sub(padding)
							} else if pid < opid {
								expVector[i] = expVector[i].Add(padding)
							}
						}
						opInfo.DcExpPadding = padding
						opInfo.DcXorRng = dcXorRng
					}

					// Now we have true exponential vector without padding.
					log.Debug("Resolve polynomial to get roots as hash of pkscript")
					if pInfo.NumMsg > 1 {
						ret, roots := flint.GetRoots(field.Prime.HexStr(), expVector[:pInfo.NumMsg], int(pInfo.NumMsg))
						log.Debugf("Func returns of peer %d: %d", ret, pid)
						log.Debugf("Number roots: %d", len(roots))
						log.Debugf("Roots: %v", roots)

						if ret != 0 {
							// This peer is malicious.
							maliciousIds = append(maliciousIds, pid)
							log.Infof("Peer is malicious: %d", pid)
							continue
						}

						// Build the dc-net exponential from messages and padding then compare dc-net vector.
						log.Infof("Total msg: %d", joinSession.TotalMsg)
						sentVector := make([]field.Field, joinSession.TotalMsg)

						msgHash := make([]field.Uint128, 0)
						for _, s := range roots {
							n, err := field.Uint128FromString(s)
							if err != nil {
								log.Errorf("Can not parse from string %v", err)
							}

							ff := field.NewFF(n)
							for i := 0; i < joinSession.TotalMsg; i++ {
								sentVector[i] = sentVector[i].Add(ff.Exp(uint64(i + 1)))
							}
							msgHash = append(msgHash, n)
						}
						slotInfo.MsgHash = msgHash

						// Padding with random number generated with secret key seed.
						for i := 0; i < int(joinSession.TotalMsg); i++ {
							// Padding with other peers.
							replayPeer := replayPeers[pid]
							for opId, opInfo := range replayPeer {
								if pid > opId {
									sentVector[i] = sentVector[i].Add(opInfo.DcExpPadding)
								} else if pid < opId {
									sentVector[i] = sentVector[i].Sub(opInfo.DcExpPadding)
								}
							}
						}

						for i := 0; i < int(joinSession.TotalMsg); i++ {
							log.Debugf("Exp vector af padding %x", sentVector[i].N.GetBytes())
						}

						for i := 0; i < int(joinSession.TotalMsg); i++ {
							//log.Debugf("compare original %x - new build dc-net %x", pInfo.DcExpVector[i].N.GetBytes(), sentVector[i].N.GetBytes())
							if bytes.Compare(pInfo.DcExpVector[i].N.GetBytes(), sentVector[i].N.GetBytes()) != 0 {
								// malicious peer
								log.Debugf("Compare dc-net vector, peer is malicious: %d", pid)
								maliciousIds = append(maliciousIds, pid)
								break
							}
						}
						peerSlotInfos[slotInfo.Id] = slotInfo
						continue
					}

					// Check whether peer dc-net vector matches with vector has sent to server.
					if pInfo.NumMsg == 1 {
						sentVector := make([]field.Field, joinSession.TotalMsg)
						ff := expVector[0]
						slotInfo.MsgHash = []field.Uint128{ff.N}
						for i := 0; i < joinSession.TotalMsg; i++ {
							sentVector[i] = sentVector[i].Add(ff.Exp(uint64(i + 1)))
						}

						for i := 0; i < int(joinSession.TotalMsg); i++ {
							log.Debugf("Exp vector bf padding %x", sentVector[i].N.GetBytes())
						}

						// Padding with random number generated with secret key seed.
						for i := 0; i < int(joinSession.TotalMsg); i++ {
							// Padding with other peers.
							replayPeer := replayPeers[pid]
							for opId, opInfo := range replayPeer {
								if pid > opId {
									sentVector[i] = sentVector[i].Add(opInfo.DcExpPadding)
								} else if pid < opId {
									sentVector[i] = sentVector[i].Sub(opInfo.DcExpPadding)
								}
							}
						}

						for i := 0; i < int(joinSession.TotalMsg); i++ {
							log.Debugf("Exp vector af padding %x", sentVector[i].N.GetBytes())
						}

						for i := 0; i < int(joinSession.TotalMsg); i++ {
							log.Debugf("compare original %x - new build dc-net %x", pInfo.DcExpVector[i].N.GetBytes(), sentVector[i].N.GetBytes())
							if bytes.Compare(pInfo.DcExpVector[i].N.GetBytes(), sentVector[i].N.GetBytes()) != 0 {
								// malicious peer
								log.Debugf("Compare dc-net vector, peer is malicious: %d", pid)
								maliciousIds = append(maliciousIds, pid)
								break
							}
						}
						peerSlotInfos[slotInfo.Id] = slotInfo
						continue
					}

				}
				log.Debug("join state", joinSession.getStateString())

				if joinSession.State == StateTxInput || joinSession.State == StateRevealSecret {
					// Client can not find message at dc-net xor round.
					// We have found all msg hash. Identify the peer's slot index.
					allMsgHash := make([]field.Uint128, 0)
					for _, slotInfo := range peerSlotInfos {
						allMsgHash = append(allMsgHash, slotInfo.MsgHash...)
					}
					sort.Slice(allMsgHash, func(i, j int) bool {
						return allMsgHash[i].Compare(allMsgHash[j]) < 0
					})

					for i, msg := range allMsgHash {
						log.Debugf("All msg, msghash :%x, Index: %d", msg.GetBytes(), i)
					}

					log.Debugf("len(allMsgHash) %d, len(peerSlotInfos) %d", len(allMsgHash), len(peerSlotInfos))
					for _, slotInfo := range peerSlotInfos {
						slotInfo.SlotIndex = make([]int, 0)
						for _, msg := range slotInfo.MsgHash {
							for i := 0; i < len(allMsgHash); i++ {
								if msg.Compare(allMsgHash[i]) == 0 {
									log.Debugf("Peer: %d, msghash :%x, Index: %d", slotInfo.Id, msg.GetBytes(), i)
									slotInfo.SlotIndex = append(slotInfo.SlotIndex, i)
								}
							}
						}

						if len(slotInfo.SlotIndex) != len(slotInfo.MsgHash) {
							// Can not find all mesages hash of peer. Terminates fail.
							log.Debug("len(slotInfo.SlotIndex) != len(slotInfo.MsgHash)")
						}
						log.Debugf("Peer %d, slotInfo.SlotIndex %v", slotInfo.Id, slotInfo.SlotIndex)
					}
					
					for _, slotInfo := range peerSlotInfos {
						slotInfo.RealMessage = make([][]byte, len(joinSession.Peers[slotInfo.Id].DcXorVector))
						copy(slotInfo.RealMessage, joinSession.Peers[slotInfo.Id].DcXorVector)
						for i, realMsg := range slotInfo.RealMessage {
							//realMsg := joinSession.Peers[slotInfo.Id].DcXorVector[i]
							log.Debugf("Peer %d, index: %d, dcxorvector message %x", slotInfo.Id, i, realMsg)
							var err error
							replayPeer := replayPeers[slotInfo.Id]
							for _, replay := range replayPeer {
								log.Debugf("Real message %x, replay.DcXorRng %x", realMsg, replay.DcXorRng)
								realMsg, err = util.XorBytes(realMsg, replay.DcXorRng)
								if err != nil {
									// Can not xor
									log.Errorf("Can not xor: %v", err)
								}
							}
							log.Debugf("Real message %x", realMsg)
							//slotInfo.RealMessage = append(slotInfo.RealMessage, realMsg)
						}
					}
					for _, slotInfo := range peerSlotInfos {
						for i := 0; i < len(slotInfo.RealMessage); i++ {
							isSlot := false
							for j := 0; j < len(slotInfo.SlotIndex); j++ {
								if i == slotInfo.SlotIndex[j] {
									log.Debugf("SlotIndex: %d", i)
									ripemdHash := ripemd128.New()
									_, err := ripemdHash.Write(slotInfo.RealMessage[i])
									if err != nil {
										// Can not write hash
									}
									hash := ripemdHash.Sum127(nil)
									log.Debugf("msgHash: %x, realmsg: %x, hash real msg: %x",
										allMsgHash[i].GetBytes(), slotInfo.RealMessage[i], hash)
									if bytes.Compare(allMsgHash[i].GetBytes(), hash) != 0 {
										// This is malicious peer
										log.Infof("Peer %d  sent invalid dc-net xor vector", slotInfo.Id)
										maliciousIds = append(maliciousIds, slotInfo.Id)
										break
									}
									isSlot = true
								}
							}
							if !isSlot {

							}
						}

					}					
					log.Debugf("End check dc-xor")
				}
			}
			joinSession.findMalicious = false
			joinSession.mu.Unlock()

			if len(maliciousIds) > 0 {
				joinSession.pushMaliciousInfo(maliciousIds)
				continue
			}
		case data := <-joinSession.msgNotFoundChan:
			joinSession.mu.Lock()
			peerInfo := joinSession.Peers[data.PeerId]
			if peerInfo == nil {
				log.Debug("joinSession %d does not include peer %d", joinSession.Id, data.PeerId)
				joinSession.mu.Unlock()
				continue
			}

			// Reveal verify key to find the malicious.
			msg := messages.NewMessage(messages.S_REVEAL_SECRET, []byte{0x00})
			for _, peer := range joinSession.Peers {
				peer.writeChan <- msg.ToBytes()
			}
			joinSession.roundTimeout = time.NewTimer(time.Second * time.Duration(joinSession.Config.RoundTimeOut))
			joinSession.State = StateRevealSecret
			joinSession.mu.Unlock()
		}
	}
	log.Infof("Session %d terminates sucessfully", joinSession.Id)
}

// randomPublisher selects random peer to publish transaction
func (joinSession *JoinSession) randomPublisher() uint32 {
	i := 0
	var publisher uint32
	randIndex := rand.Intn(len(joinSession.Peers))
	for _, peer := range joinSession.Peers {
		if i == randIndex {
			publisher = peer.Id
			break
		}
		i++
	}
	return publisher
}

// getStateString converts join session state value to string.
func (joinSession *JoinSession) getStateString() string {
	state := string(joinSession.State)

	switch joinSession.State {
	case 1:
		state = "StateKeyExchange"
	case 2:
		state = "StateDcExponential"
	case 3:
		state = "StateDcXor"
	case 4:
		state = "StateTxInput"
	case 5:
		state = "StateTxSign"
	case 6:
		state = "StateTxPublish"
	case 7:
		state = "StateCompleted"
	case 8:
		state = "StateRevealSecret"
	}
	return state
}
