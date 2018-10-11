package coinjoin

import (
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/gogo/protobuf/proto"

	//"github.com/decred/dcrd/wire"
	"bytes"

	"math/rand"

	"github.com/decred/dcrd/wire"
	pb "github.com/raedahgroup/dcrtxmatcher/api/messages"
	"github.com/raedahgroup/dcrtxmatcher/finitefield"
	"github.com/raedahgroup/dcrtxmatcher/flint"
	"github.com/raedahgroup/dcrtxmatcher/util"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

type (
	JoinSession struct {
		Id uint32
		sync.Mutex
		Peers   map[uint32]*PeerInfo
		DiceMix *DiceMix
		State   int

		PKs      []*pb.PeerInfo
		JoinedTx *wire.MsgTx

		Publisher uint32

		keyExchangeChan     chan pb.KeyExchangeReq
		dcExpVectorChan     chan pb.DcExpVector
		dcXorVectorChan     chan pb.DcXorVector
		txInputsChan        chan pb.TxInputs
		txSignedTxChan      chan pb.JoinTx
		txPublishResultChan chan []byte
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
	var allmsgs [][]byte
	for {
		select {

		case req := <-joinSession.keyExchangeChan:

			joinSession.Lock()
			peer := joinSession.Peers[req.PeerId]

			if peer == nil {
				//send error to client
				log.Debug("<-joinSession.keyExchangeChan peer is nil")

			} else {
				if len(peer.PK) == 0 {
					peer.PK = req.Pk
					peer.NumMsg = req.NumMsg
					joinSession.PKs = append(joinSession.PKs, &pb.PeerInfo{PeerId: peer.Id, Pk: peer.PK})
				}

				//if there is enough pk, broadcast pks to all clients
				if len(joinSession.Peers) == len(joinSession.PKs) {
					keyex := &pb.KeyExchangeRes{
						Peers:  joinSession.PKs,
						NumMsg: req.NumMsg,
					}

					data, err := proto.Marshal(keyex)
					if err != nil {
						log.Errorf("proto.Marshal keyexchange error: %v", err)
						break
					}

					message := NewMessage(S_KEY_EXCHANGE, data)

					for _, p := range joinSession.Peers {
						p.writeChan <- message.ToBytes()
					}
				}
			}
			joinSession.Unlock()
		case data := <-joinSession.dcExpVectorChan:
			joinSession.Lock()
			peerInfo := joinSession.Peers[data.PeerId]
			if peerInfo == nil {
				fmt.Println("dcExpVector not include peerid")
			}
			fmt.Println("dcExpVectorChan data.Len", data.Len)

			vector := make([]field.Field, 0)
			for i := 0; i < int(data.Len); i++ {
				b := data.Vector[i*16 : (i+1)*16]
				ff := field.NewFF(field.FromBytes(b))
				vector = append(vector, ff)
			}

			peerInfo.DcExpVector = vector
			peerInfo.Commit = data.Commit

			fmt.Printf("commit %x\n", peerInfo.Commit)
			fmt.Printf("vector size %x\n", len(vector))

			allSubmit := true
			for _, peer := range joinSession.Peers {
				if len(peer.DcExpVector) == 0 {
					allSubmit = false
				}
			}

			//solve polynomial to get roots
			if allSubmit {
				poly_degree := len(vector)
				dc_combine := make([]field.Field, poly_degree)

				for _, peer := range joinSession.Peers {
					for i := 0; i < len(peer.DcExpVector); i++ {
						dc_combine[i] = dc_combine[i].Add(peer.DcExpVector[i])
					}

				}

				for _, ff := range dc_combine {
					fmt.Println("dc-combine:", ff.N.HexStr())
				}

				ret, roots := flint.GetRoots(field.Prime.HexStr(), dc_combine, poly_degree)
				fmt.Printf("ret %d. number roots: %d, roots: %v\n", ret, len(roots), roots)

				//send back to all peers
				allMsgHash := make([]byte, 0)

				for _, root := range roots {
					str := fmt.Sprintf("%032v", root)
					bytes, err := hex.DecodeString(str)
					if err != nil {
						fmt.Errorf("error DecodeString %v", err)
					}
					fmt.Println("size of root in bytes ", len(bytes))
					allMsgHash = append(allMsgHash, bytes...)
				}

				msgdata := &pb.AllMessages{}
				msgdata.Len = uint32(len(roots))
				msgdata.Msgs = allMsgHash

				data, err := proto.Marshal(msgdata)
				if err != nil {
					log.Errorf("proto.Marshal keyexchange error: %v", err)
					break
				}

				msg := NewMessage(S_DC_EXP_VECTOR, data)

				//broadcast all peers
				for _, peer := range joinSession.Peers {
					peer.writeChan <- msg.ToBytes()
				}

			}

			joinSession.Unlock()

		case data := <-joinSession.dcXorVectorChan:
			joinSession.Lock()
			dcXor := make([][]byte, 0)
			for i := 0; i < int(data.Len); i++ {
				msg := data.Vector[i*25 : (i+1)*25]
				dcXor = append(dcXor, msg)
			}

			peerInfo := joinSession.Peers[data.PeerId]
			if peerInfo == nil {
				fmt.Println("dcExpVector not include peerid")
			}

			peerInfo.DcXorVector = dcXor

			fmt.Println("dcXor len ", len(dcXor))

			allSubmit := true
			for _, peer := range joinSession.Peers {
				if len(peer.DcXorVector) == 0 {
					allSubmit = false
					break
				}
			}

			//solve xor vector to get all messages
			allmsgs = make([][]byte, len(peerInfo.DcXorVector))
			var err error = nil
			if allSubmit {
				for i := 0; i < len(peerInfo.DcXorVector); i++ {
					for _, peer := range joinSession.Peers {
						allmsgs[i], err = util.XorBytes(allmsgs[i], peer.DcXorVector[i])
						if err != nil {
							fmt.Errorf("error XorBytes %v", err)
						}
					}
				}
			}
			if allSubmit {
				for _, msg := range allmsgs {
					fmt.Printf("allmsgs %x\n", msg)
				}

				//broadcast result to peers in session
				dcxorRet := &pb.DcXorVectorResult{}
				dcxorData, err := proto.Marshal(dcxorRet)
				if err != nil {
					fmt.Errorf("error Marshal DcXorVectorResult %v\n", err)
				}

				message := NewMessage(S_DC_XOR_VECTOR, dcxorData)

				for _, peer := range joinSession.Peers {
					peer.writeChan <- message.ToBytes()
				}

			}
			joinSession.Unlock()

		case txins := <-joinSession.txInputsChan:
			peer := joinSession.Peers[txins.PeerId]
			peer.TicketPrice = txins.TicketPrice

			var tx wire.MsgTx
			buf := bytes.NewReader(txins.Txins)
			err := tx.BtcDecode(buf, 0)
			if err != nil {
				fmt.Errorf("error BtcDecode %v\n", err)
				break
			}

			peer.TxIns = &tx

			fmt.Println("Number txins", len(tx.TxIn), len(tx.TxOut))
			allSubmit := true
			for _, peer := range joinSession.Peers {
				if peer.TxIns == nil {
					allSubmit = false
					break
				}
			}

			//build the transaction
			var joinedtx *wire.MsgTx
			if allSubmit {
				for _, peer := range joinSession.Peers {
					if joinedtx == nil {
						joinedtx = peer.TxIns
						for i := range peer.TxIns.TxIn {
							peer.InputIndex = append(peer.InputIndex, i)
						}

						fmt.Println("peer InputIndex", peer.Id, peer.InputIndex)

					} else {
						endIndex := len(joinedtx.TxIn)
						joinedtx.TxIn = append(joinedtx.TxIn, peer.TxIns.TxIn...)
						joinedtx.TxOut = append(joinedtx.TxOut, peer.TxIns.TxOut...)

						for i := range peer.TxIns.TxIn {
							peer.InputIndex = append(peer.InputIndex, i+endIndex)
						}
						fmt.Println("peer InputIndex 1", peer.Id, peer.InputIndex)

					}
				}
				for _, msg := range allmsgs {
					txout := wire.NewTxOut(peer.TicketPrice, msg)
					joinedtx.AddTxOut(txout)
				}
				fmt.Println("len of txout, txin", len(joinedtx.TxIn), len(joinedtx.TxOut))

				for _, joinTxin := range joinedtx.TxIn {
					fmt.Println("joinSession.txInputsChan PreviousOutPoint.Hash outpoint ", joinTxin.PreviousOutPoint.Hash.String())
				}
				//send joinedtx back to peers and peers will sign tx

				buffTx := bytes.NewBuffer(nil)
				buffTx.Grow(joinedtx.SerializeSize())
				err := joinedtx.BtcEncode(buffTx, 0)
				if err != nil {
					fmt.Errorf("error BtcEncode %v", err)
					break
				}

				joinTx := &pb.JoinTx{}
				joinTx.Tx = buffTx.Bytes()

				joinTxData, err := proto.Marshal(joinTx)
				if err != nil {

				}

				joinTxMsg := NewMessage(S_JOINED_TX, joinTxData)
				for _, peer := range joinSession.Peers {
					peer.writeChan <- joinTxMsg.ToBytes()
				}

			}
		case signedTx := <-joinSession.txSignedTxChan:
			peer := joinSession.Peers[signedTx.PeerId]
			fmt.Println("txSignedTxChan")
			var tx wire.MsgTx
			reader := bytes.NewReader(signedTx.Tx)
			err := tx.BtcDecode(reader, 0)
			if err != nil {

			}
			if peer.SignedTx != nil {
				continue
			}
			peer.SignedTx = &tx

			fmt.Println("len signedTx txin", len(tx.TxIn), len(tx.TxOut))

			if joinSession.JoinedTx == nil {
				fmt.Println("peer submit signed tx, peerid, index", peer.Id, peer.InputIndex)
				joinSession.JoinedTx = tx.Copy()
			} else {
				fmt.Println("peer submit later signed tx peerid, index", peer.Id, peer.InputIndex)
				for _, index := range peer.InputIndex {
					fmt.Println("peer id, index", peer.Id, index)
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
				fmt.Println("len signedTx txin", len(joinSession.JoinedTx.TxIn), len(joinSession.JoinedTx.TxIn))
				//send jointx to peers and set publisher
				buffTx := bytes.NewBuffer(nil)
				buffTx.Grow(joinSession.JoinedTx.SerializeSize())
				for _, joinTxin := range joinSession.JoinedTx.TxIn {
					fmt.Println("bf pub txSignedTxChan.PreviousOutPoint.Hash outpoint ", joinTxin.PreviousOutPoint.Hash.String())
				}
				err := joinSession.JoinedTx.BtcEncode(buffTx, 0)
				if err != nil {
					fmt.Errorf("error BtcEncode %v", err)
					break
				}

				joinTx := &pb.JoinTx{}
				joinTx.Tx = buffTx.Bytes()

				joinTxData, err := proto.Marshal(joinTx)
				if err != nil {

				}

				publisher := rand.Intn(len(joinSession.Peers))

				fmt.Println("publisher index ", publisher)

				joinTxMsg := NewMessage(S_TX_SIGN, joinTxData)
				n := 0
				for _, peer := range joinSession.Peers {
					if n == publisher {
						peer.Publisher = true
						fmt.Println("publisher peerId ", peer.Id)
						joinSession.Publisher = peer.Id
						peer.writeChan <- joinTxMsg.ToBytes()
						break
					}
					n++
				}

			}

		case pubResult := <-joinSession.txPublishResultChan:
			fmt.Println("len pubResult ", len(pubResult))

			msg := NewMessage(S_TX_PUBLISH_RESULT, pubResult)
			for _, peer := range joinSession.Peers {
				peer.writeChan <- msg.ToBytes()
			}

		}
	}
}
