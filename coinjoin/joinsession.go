package coinjoin

import (
	"bytes"
	"crypto/elliptic"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"

	"math/rand"
	"net/http"
	"sort"
	"strings"
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

		PeersMsgInfo     []*pb.PeerInfo
		JoinedTx         *wire.MsgTx
		Publisher        uint32
		maliciousFinding bool

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
	peer, ok := joinSession.Peers[peerId]
	if ok {
		delete(joinSession.Peers, peerId)
		if peer != nil {
			peer.Conn.Close()
		}
		log.Infof("Remove peer %d from join session", peerId)
	}
}

// pushMaliciousInfo removes peer ids from join session and disconnects.
// Also generates new session id, new peer id for each remaining peer
// to start the join session from beginning.
func (joinSession *JoinSession) pushMaliciousInfo(missedPeers []uint32) {

	joinSession.mu.Lock()
	defer joinSession.mu.Unlock()
	log.Debug("Number malicious", len(missedPeers))
	malicious := &pb.MaliciousPeers{}
	for _, Id := range missedPeers {
		joinSession.removePeer(Id)
	}
	malicious.PeerIds = missedPeers

	if len(joinSession.Peers) == 0 {
		log.Info("All peers not sending data in time, join session terminates")
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
		msg := messages.NewMessage(messages.S_MALICIOUS_PEERS, peer.TmpData).ToBytes()
		peer.writeChan <- msg
	}

	log.Debug("Remaining peers in join session after updated: ", joinSession.Peers)
}

// terminate release resource and disconnect remaining peers.
func (joinSession *JoinSession) terminate() {
	for _, peer := range joinSession.Peers {
		peer.Conn.Close()
	}
}

// publishTxHex publishes the transaction encoded in hexa via restful API with url parameter.
// The restful API is usually dcrdata API. This func is called by publishTx.
func publishTxHex(txHex, url string) error {
	// Call dcrdata api endpoint to publish tx.
	jsonData := map[string]interface{}{
		"rawtx": txHex,
	}

	jsonBytes, err := json.Marshal(jsonData)
	if err != nil {
		log.Errorf("Can not Marshal json data %v", err)
		return err
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBytes))
	if err != nil {
		log.Errorf("Can not Post json data %v", err)
		return err
	}
	respBody, err := ioutil.ReadAll(resp.Body)

	ret := string(respBody)
	if !strings.Contains(ret, "txid") {
		return errors.New(ret)
	}

	return nil
}

// publishTx publishes the transaction via restful API with url parameter.
// The restful API is usually dcrdata API.
func publishTx(ptx *wire.MsgTx, url string) error {

	writer := bytes.NewBuffer(nil)
	err := ptx.Serialize(writer)
	if err != nil {
		log.Errorf("Can not Serialize transaction %v", err)
		return err
	}

	txHex := hex.EncodeToString(writer.Bytes())
	return publishTxHex(txHex, url)
}

// run checks for join session's incoming data.
// Each peer sends data, server receives and processes data then
// sends back peers the information for next round.
func (joinSession *JoinSession) run() {
	var allPkScripts [][]byte
	missedPeers := make([]uint32, 0)
	var errm error
	// Stop round timer
	defer joinSession.roundTimeout.Stop()
LOOP:
	for {
		if len(joinSession.Peers) == 0 {
			log.Infof("No peer connected, session %d terminates.", joinSession.Id)
			return
		}
		select {
		case <-joinSession.roundTimeout.C:
			joinSession.mu.Lock()
			log.Info("Timeout.")
			// We use timer to control whether peers send data of round in time.
			// With one round of the join session, server waits maximum time
			// for client process is 60 seconds (setting in config file).
			// After that time, client still not send data,
			// server will consider the client is malicious and remove from the join session.
			for _, peer := range joinSession.Peers {
				switch joinSession.State {
				case StateKeyExchange:
					// With state key exchange, we do not need to inform other peers,
					// just remove and ignore this peer.
					if len(peer.Pk) == 0 {
						missedPeers = append(missedPeers, peer.Id)
						//joinSession.removePeer(peer.Id)
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
					if joinSession.Publisher == peer.Id {
						log.Infof("Peer id %v did not send the published transaction data in time", peer.Id)
						joinSession.removePeer(peer.Id)

						if len(joinSession.Peers) == 0 {
							log.Infof("No peer connected, session %d terminates.", joinSession.Id)
							joinSession.mu.Unlock()
							return
						}
						// Select other peer to publish transaction.
						buffTx := bytes.NewBuffer(nil)
						buffTx.Grow(joinSession.JoinedTx.SerializeSize())
						err := joinSession.JoinedTx.BtcEncode(buffTx, 0)
						if err != nil {
							log.Errorf("Cannot execute BtcEncode: %v", err)
							joinSession.terminate()
							joinSession.mu.Unlock()
							return
						}

						joinTx := &pb.JoinTx{}
						joinTx.Tx = buffTx.Bytes()
						joinTxData, err := proto.Marshal(joinTx)
						if err != nil {
							log.Errorf("Can not marshal signed transaction: %v", err)
							log.Infof("Session terminates fail")
							joinSession.terminate()
							joinSession.mu.Unlock()
							return
						}
						joinTxMsg := messages.NewMessage(messages.S_TX_SIGN, joinTxData)
						pubId := joinSession.randomPublisher()
						joinSession.Publisher = pubId
						publisher := joinSession.Peers[pubId]
						publisher.writeChan <- joinTxMsg.ToBytes()
						joinSession.roundTimeout = time.NewTimer(time.Second * time.Duration(joinSession.Config.RoundTimeOut))
					}
				case StateRevealSecret:
					if len(peer.Vk) == 0 {
						missedPeers = append(missedPeers, peer.Id)
						log.Infof("Peer id %d did not send secret key in time", peer.Id)
					}
				}
			}

			// Inform to remaining peers in join session.
			if len(missedPeers) > 0 {
				if joinSession.State == StateKeyExchange {
					for _, id := range missedPeers {
						joinSession.removePeer(id)
					}
				} else {
					joinSession.pushMaliciousInfo(missedPeers)
					// Reset join session state.
					joinSession.State = StateKeyExchange
				}
			}
			joinSession.mu.Unlock()
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

			log.Debug("Received DH public key exchange request from peer", peer.Id)

			// Broadcast to all peers when there are enough public keys.
			if len(joinSession.Peers) == len(joinSession.PeersMsgInfo) {

				log.Debug("Received DH public key from all peers. Broadcasting all DH public keys to all peers.")
				log.Debug("Each peer will combine their DH private key with the other public keys to derive the random bytes.")
				log.Debug("Random bytes are being used to creating padding in the DC-net.")
				keyex := &pb.KeyExchangeRes{
					Peers: joinSession.PeersMsgInfo,
				}

				data, err := proto.Marshal(keyex)
				if err != nil {
					log.Errorf("Can not marshal keyexchange: %v", err)
					// Public key is invalid
					joinSession.removePeer(keyExchange.PeerId)
					joinSession.mu.Unlock()
					continue
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
			if len(data.Vector) < messages.PkScriptHashSize*2 {
				// Invalid dcxor vector length.
				joinSession.mu.Unlock()
				joinSession.pushMaliciousInfo([]uint32{data.PeerId})
				break LOOP
			}
			vector := make([]field.Field, 0)
			for i := 0; i < int(data.Len); i++ {
				b := data.Vector[i*messages.PkScriptHashSize : (i+1)*messages.PkScriptHashSize]
				ff := field.NewFF(field.FromBytes(b))
				// Validate pkscript hash
				if ff.N.Compare(field.Uint128{0, 0}) == 0 {
					errMsg := fmt.Sprintf("Client %d submitted invalid pkscript hash %s", peerInfo.Id, ff.HexStr())
					errm = errors.New(errMsg)
					log.Warnf(errMsg)
					break
				}
				vector = append(vector, ff)
				//log.Debugf("Received dc-net exp vector %d - %x", peerInfo.Id, b)
			}
			if errm != nil {
				joinSession.mu.Unlock()
				joinSession.pushMaliciousInfo([]uint32{peerInfo.Id})
				continue
			}

			peerInfo.DcExpVector = vector
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
				// for _, ff := range dcCombine {
				// 	log.Debug("Dc-combine:", ff.N.HexStr())
				// }
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
						errMsg := fmt.Sprintf("Got pkscript hash from flint with size %d - %x. This differs with default size: %d",
							len(bytes), bytes, messages.PkScriptHashSize)
						log.Warnf(errMsg)
						errm = errors.New(errMsg)
						break
					}
				}
				if errm != nil {
					break LOOP
				}

				msgData := &pb.AllMessages{}
				msgData.Len = uint32(len(roots))
				msgData.Msgs = allMsgHash
				data, err := proto.Marshal(msgData)
				if err != nil {
					log.Errorf("Can not marshal all messages data: %v", err)
					errm = err
					break LOOP
				}

				msg := messages.NewMessage(messages.S_DC_EXP_VECTOR, data)
				for _, peer := range joinSession.Peers {
					peer.writeChan <- msg.ToBytes()
				}
				joinSession.roundTimeout = time.NewTimer(time.Second * time.Duration(joinSession.Config.RoundTimeOut))
				joinSession.State = StateDcXor
			}
			joinSession.mu.Unlock()
		case data := <-joinSession.dcXorVectorChan:
			joinSession.mu.Lock()
			dcXor := make([][]byte, 0)
			if len(data.Vector) < messages.PkScriptSize*2 {
				// Invalid dcxor vector length.
				joinSession.mu.Unlock()
				joinSession.pushMaliciousInfo([]uint32{data.PeerId})
				break LOOP
			}
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
				// Each peer will send [pkscript ^ P ^ P1 ^ P2...] bytes to server.
				// Of these, Pi is random padding bytes between peer and peer i.
				// Server combines all xor vectors. Thereafter it has [pkscript ^ (P ^ P1 ^ P2...) ^ (P ^ P1 ^ P2...)] = pkscript.
				// Server can not know pkscripts belong to which peer because only a peer knows its slot index.
				// After resolving dc-net xor, just only a peer knows its own pkscript.
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
					errm = err
					break LOOP
				}

				log.Debug("Solved dc-net xor vector and got all pkscripts")
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

			// Validate ticket price
			if peer.TicketPrice <= 0 {
				errMsg := fmt.Sprintf("Peer %d sent invalid ticket price: %d", peer.Id, peer.TicketPrice)
				log.Warnf(errMsg)
				joinSession.mu.Unlock()
				joinSession.pushMaliciousInfo([]uint32{peer.Id})
				break LOOP
			}

			var tx wire.MsgTx
			buf := bytes.NewReader(txins.Txins)
			err := tx.BtcDecode(buf, 0)
			if err != nil {
				log.Errorf("error BtcDecode %v", err)
				errm = err
				break LOOP
			}
			peer.TxIns = &tx

			// Validate tx input
			errValidation := false
			var txAmount int64
			for _, txin := range tx.TxIn {
				if len(txin.SignatureScript) > 0 {
					log.Errorf("Peer %d have sent tx already signed", peer.Id)
					errm = errors.New("")
					break
				}
				txAmount += txin.ValueIn
			}
			if errValidation {
				joinSession.mu.Unlock()
				joinSession.pushMaliciousInfo([]uint32{peer.Id})
				continue
			}
			// Validate tx amount
			txoutValue := (tx.TxOut[0].Value + int64(peer.NumMsg)*peer.TicketPrice)
			var txinAmout float64 = float64(txAmount) / 100000000
			var txoutAmount float64 = float64(txoutValue) / 100000000

			log.Debugf("Peer %d, %d inputs (%f DCR), %d output for (%f DCR), fee (%f DCR)",
				peer.Id, len(tx.TxIn), txinAmout, len(tx.TxOut), txoutAmount, txinAmout-txoutAmount)

			if txoutAmount > txinAmout {
				log.Errorf("Peer %d spent utxo amount %f greater than txin amount %f", peer.Id, txoutAmount, txinAmout)
				errValidation = true
				break
			}
			if errValidation {
				joinSession.mu.Unlock()
				joinSession.pushMaliciousInfo([]uint32{peer.Id})
				errm = nil
				continue
			}

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
				log.Debug("All peers sent txin and txout change amount, will create join tx for signing")
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
					errm = err
					break LOOP
				}

				joinTx := &pb.JoinTx{}
				joinTx.Tx = buffTx.Bytes()
				joinTxData, err := proto.Marshal(joinTx)
				if err != nil {
					log.Errorf("Can not marshal transaction: %v", err)
					errm = err
					break LOOP
				}
				joinTxMsg := messages.NewMessage(messages.S_JOINED_TX, joinTxData)
				for _, peer := range joinSession.Peers {
					peer.writeChan <- joinTxMsg.ToBytes()
				}
				log.Debug("Server built join tx from txin that just received, txout is created from the resolved pkscripts and ticket price")
				log.Debug("Broadcast the joint transaction to all peers.")
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
				log.Errorf("Can not decode transaction: %v", err)
				log.Infof("Session %d terminates fail", joinSession.Id)
				errm = err
				break LOOP
			}
			if peer.SignedTx != nil {
				joinSession.mu.Unlock()
				continue
			}
			peer.SignedTx = &tx
			log.Debug("Received signed transaction from peer", peer.Id)

			// Validate signed tx to match with previous sent txin
			i := 0
			errValidation := false
			for _, idx := range peer.InputIndex {
				if tx.TxIn[idx].ValueIn != peer.TxIns.TxIn[i].ValueIn ||
					tx.TxIn[idx].PreviousOutPoint.Hash.String() != peer.TxIns.TxIn[i].PreviousOutPoint.Hash.String() ||
					tx.TxIn[idx].PreviousOutPoint.Index != peer.TxIns.TxIn[i].PreviousOutPoint.Index ||
					len(tx.TxIn[idx].SignatureScript) == 0 {
					log.Errorf("Peer %d sent invalid signed tx data", peer.Id)
					errValidation = true
					break
				}
				i++
			}
			if errValidation {
				joinSession.mu.Unlock()
				joinSession.pushMaliciousInfo([]uint32{peer.Id})
				continue
			}

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
				log.Info("Applied signatures from all peers.")

				buffTx := bytes.NewBuffer(nil)
				buffTx.Grow(joinSession.JoinedTx.SerializeSize())

				err := joinSession.JoinedTx.BtcEncode(buffTx, 0)
				if err != nil {
					log.Errorf("Cannot execute BtcEncode: %v", err)
					log.Infof("Session %d terminates fail", joinSession.Id)
					errm = err
					break LOOP
				}
				published := false
				if joinSession.Config.ServerPublish {
					// publish transaction from server
					url := "https://testnet.dcrdata.org/insight/api/tx/send"
					log.Infof("Will broadcast transaction via %s", url)
					cnt := 0
					for {
						err := publishTx(joinSession.JoinedTx, url)
						if err == nil {
							msg := messages.NewMessage(messages.S_TX_PUBLISH_RESULT, buffTx.Bytes())
							for _, peer := range joinSession.Peers {
								peer.writeChan <- msg.ToBytes()
							}
							joinSession.State = StateCompleted
							joinSession.mu.Unlock()
							log.Infof("Transaction %s was published.", joinSession.JoinedTx.TxHash().String())
							log.Info("Broadcast the joint transaction to all peers.")
							published = true
							break
						} else {
							log.Warnf("Can not publish transaction from server side: %v", err)
						}
						cnt++
						if cnt == 3 {
							break
						}
						time.Sleep(time.Second * 5)
					}
				}
				if published {
					log.Infof("Session %d terminates successfully.", joinSession.Id)
					break LOOP
				}
				log.Warnf("Will publish from client side.")
				joinTx := &pb.JoinTx{}
				joinTx.Tx = buffTx.Bytes()
				joinTxData, err := proto.Marshal(joinTx)
				if err != nil {
					log.Errorf("Can not marshal signed transaction: %v", err)
					log.Infof("Session %d terminates fail.", joinSession.Id)
					errm = err
					break LOOP
				}

				joinTxMsg := messages.NewMessage(messages.S_TX_SIGN, joinTxData)
				pubId := joinSession.randomPublisher()
				joinSession.Publisher = pubId
				publisher := joinSession.Peers[pubId]
				log.Infof("Peer %d is randomly selected to publish transaction %s", pubId, joinSession.JoinedTx.TxHash().String())
				publisher.writeChan <- joinTxMsg.ToBytes()
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
			log.Info("Broadcast the join transaction to all peers.")
			log.Infof("Session %d terminates successfully.", joinSession.Id)
			break LOOP
		case rvSecret := <-joinSession.revealSecretChan:
			// Save verify key
			joinSession.mu.Lock()
			peer := joinSession.Peers[rvSecret.PeerId]
			if peer == nil {
				joinSession.mu.Unlock()
				log.Debug("joinSession %d does not include peer %d", joinSession.Id, rvSecret.PeerId)
				continue
			}
			peer.Vk = rvSecret.Vk
			log.Debugf("Peer %d submit verify key %x", peer.Id, peer.Vk)

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
				replayPeers := make(map[uint32]map[uint32]*PeerReplayInfo, 0)
				ecp256 := ecdh.NewEllipticECDH(elliptic.P256())

				// We use simple method by generating key pair of server
				// then compare share key with each peer.
				for _, p := range joinSession.Peers {
					peerPrivKey := ecp256.UnmarshalPrivateKey(p.Vk)
					peerPubKey, _ := ecp256.Unmarshal(p.Pk)
					serverPrivKey, serverPubKey, err := ecp256.GenerateKey(crand.Reader)

					shareKey1, err := ecp256.GenSharedSecret(peerPrivKey, serverPubKey)
					if err != nil {
						errMsg := fmt.Sprintf("Can not generate shared secret key: %v", err)
						log.Errorf(errMsg)
						errm = errors.New(errMsg)
						break
					}
					shareKey2, err := ecp256.GenSharedSecret(serverPrivKey, peerPubKey)
					if err != nil {
						errMsg := fmt.Sprintf("Can not generate shared secret key: %v", err)
						log.Errorf(errMsg)
						errm = errors.New(errMsg)
						break
					}

					if bytes.Compare(shareKey1, shareKey2) != 0 {
						maliciousIds = append(maliciousIds, p.Id)
						log.Infof("Peer %d is malicious - sent invalid public/verify key pair.", p.Id)
					}
				}
				if errm != nil {
					break LOOP
				}

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
							errMsg := fmt.Sprintf("Can not generate shared secret key: %v", err)
							log.Errorf(errMsg)
							errm = errors.New(errMsg)
							break
						}

						opeerInfo.SharedKey = sharedKey
						opeerInfo.Id = op.Id
						replayPeer[op.Id] = opeerInfo
					}
					if errm != nil {
						break LOOP
					}
					replayPeers[p.Id] = replayPeer
					joinSession.TotalMsg += int(p.NumMsg)
				}

				// Maintain a counter the number incorrect share key of each peer.
				// If one peer with more than one incorrect share key then
				// the peer is malicious.
				// compareCount := make(map[uint32]int)
				// for pid, replayPeer := range replayPeers {
				// 	for opid, opReplayPeer := range replayPeers {
				// 		if pid == opid {
				// 			continue
				// 		}

				// 		pInfo := opReplayPeer[pid]
				// 		opInfo := replayPeer[opid]
				// 		if bytes.Compare(pInfo.SharedKey, opInfo.SharedKey) != 0 {
				// 			log.Debugf("compare vk %x of peer %d with vk %x of peer %d", pInfo.SharedKey, pInfo.Id, opInfo.SharedKey, opInfo.Id)
				// 			// Increase compare counter
				// 			if _, ok := compareCount[pid]; ok {
				// 				compareCount[pid] = compareCount[pid] + 1
				// 			} else {
				// 				compareCount[pid] = 1
				// 			}

				// 			if _, ok := compareCount[opid]; ok {
				// 				compareCount[opid] = compareCount[opid] + 1
				// 			} else {
				// 				compareCount[opid] = 1
				// 			}
				// 		}
				// 	}
				// }

				// for pid, count := range compareCount {
				// 	log.Debug("Peer %d, compare counter %d", pid, count)
				// 	// Total number checking of share key is wrong
				// 	// means this peer submit invalid pk/vk key pair.
				// 	if count >= len(joinSession.Peers)-1 {
				// 		// Peer is malicious
				// 		maliciousIds = append(maliciousIds, pid)
				// 		log.Infof("Peer %d is malicious - sent invalid public/verify key pair", pid)
				// 	}
				// }

				// Share keys are correct. Check dc-net exponential and dc-net xor vector.
				peerSlotInfos := make(map[uint32]*PeerSlotInfo, 0)
				for pid, pInfo := range joinSession.Peers {
					replayPeer := replayPeers[pid]

					expVector := make([]field.Field, len(pInfo.DcExpVector))
					copy(expVector, pInfo.DcExpVector)
					slotInfo := &PeerSlotInfo{Id: pid}
					for opid, opInfo := range replayPeer {
						dcexpRng, err := chacharng.RandBytes(opInfo.SharedKey, messages.ExpRandSize)
						if err != nil {
							if err != nil {
								log.Errorf("Can generate random bytes: %v", err)
								errm = err
								break LOOP
							}
						}
						dcexpRng = append([]byte{0, 0, 0, 0}, dcexpRng...)

						// For random byte of Xor vector, we get the same size of pkscript is 25 bytes
						dcXorRng, err := chacharng.RandBytes(opInfo.SharedKey, messages.PkScriptSize)
						if err != nil {
							if err != nil {
								log.Errorf("Can generate random bytes: %v", err)
								errm = err
								break LOOP
							}
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

					// Now we have real exponential vector without padding.
					// If dc-net exponential is valid then polynomial of peer could be solved.
					log.Debug("Check the validity of dc-net exponential of peer by resolving polynomial.")
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
						slotInfo.PkScriptsHash = msgHash

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
						slotInfo.PkScriptsHash = []field.Uint128{ff.N}
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
						allMsgHash = append(allMsgHash, slotInfo.PkScriptsHash...)
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
						for _, msg := range slotInfo.PkScriptsHash {
							for i := 0; i < len(allMsgHash); i++ {
								if msg.Compare(allMsgHash[i]) == 0 {
									log.Debugf("Peer: %d, msghash :%x, Index: %d", slotInfo.Id, msg.GetBytes(), i)
									slotInfo.SlotIndex = append(slotInfo.SlotIndex, i)
								}
							}
						}

						if len(slotInfo.SlotIndex) != len(slotInfo.PkScriptsHash) {
							// Can not find all mesages hash of peer. Terminates fail.
							log.Debug("len(slotInfo.SlotIndex) != len(slotInfo.MsgHash)")
						}
						log.Debugf("Peer %d, slotInfo.SlotIndex %v", slotInfo.Id, slotInfo.SlotIndex)
					}

					for _, slotInfo := range peerSlotInfos {
						slotInfo.PkScripts = make([][]byte, 0)
						for i, realMsg := range joinSession.Peers[slotInfo.Id].DcXorVector {
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
							slotInfo.PkScripts = append(slotInfo.PkScripts, realMsg)
						}
					}
					for _, slotInfo := range peerSlotInfos {
					LOOPRV:
						for i := 0; i < len(slotInfo.PkScripts); i++ {
							isSlot := false
							for j := 0; j < len(slotInfo.SlotIndex); j++ {
								if i == slotInfo.SlotIndex[j] {
									log.Debugf("SlotIndex: %d", i)
									ripemdHash := ripemd128.New()
									_, err := ripemdHash.Write(slotInfo.PkScripts[i])
									if err != nil {
										// Can not write hash
									}
									hash := ripemdHash.Sum127(nil)
									log.Debugf("msgHash: %x, realmsg: %x, hash real msg: %x",
										allMsgHash[i].GetBytes(), slotInfo.PkScripts[i], hash)
									if bytes.Compare(allMsgHash[i].GetBytes(), hash) != 0 {
										// This is malicious peer
										log.Infof("Peer %d sent invalid dc-net xor vector on non-slot %d", slotInfo.Id, i)
										maliciousIds = append(maliciousIds, slotInfo.Id)
										break LOOPRV
									}
									isSlot = true
								}
							}
							if !isSlot {
								sample := "00000000000000000000000000000000000000000000000000"
								pkScript := hex.EncodeToString(slotInfo.PkScripts[i])
								if strings.Compare(sample, pkScript) != 0 {
									log.Infof("Peer %d sent invalid dc-net xor vector on non-slot %d", slotInfo.Id, i)
									maliciousIds = append(maliciousIds, slotInfo.Id)
									break LOOPRV
								}
							}
						}
					}
					log.Debugf("End check dc-xor")
				}
			}
			joinSession.maliciousFinding = false
			joinSession.mu.Unlock()

			if len(maliciousIds) > 0 {
				joinSession.pushMaliciousInfo(maliciousIds)
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
	if errm != nil {
		log.Errorf("Session %d terminate fail with error %v", errm)
		joinSession.mu.Unlock()
		joinSession.terminate()
	}

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
