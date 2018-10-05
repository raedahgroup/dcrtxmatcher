package coinjoin

import (
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/gogo/protobuf/proto"

	pb "github.com/raedahgroup/dcrtxmatcher/api/messages"
	"github.com/raedahgroup/dcrtxmatcher/finitefield"
	"github.com/raedahgroup/dcrtxmatcher/flint"
	"github.com/raedahgroup/dcrtxmatcher/util"
)

type (
	JoinSession struct {
		Id uint32
		sync.Mutex
		Peers   map[uint32]*PeerInfo
		DiceMix *DiceMix
		State   int

		PKs []*pb.PeerInfo

		keyExchangeChan chan pb.KeyExchangeReq
		dcExpVectorChan chan pb.DcExpVector
		dcXorVectorChan chan pb.DcXorVector
	}
)

func NewJoinSession(sessionId uint32) *JoinSession {
	return &JoinSession{
		Id:              sessionId,
		Peers:           make(map[uint32]*PeerInfo),
		keyExchangeChan: make(chan pb.KeyExchangeReq),
		dcExpVectorChan: make(chan pb.DcExpVector),
		dcXorVectorChan: make(chan pb.DcXorVector),
		PKs:             make([]*pb.PeerInfo, 0),
	}
}

func (joinSession *JoinSession) run() {

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
				allmsgs := make([]byte, 0)

				for _, root := range roots {
					str := fmt.Sprintf("%032v", root)
					bytes, err := hex.DecodeString(str)
					if err != nil {
						fmt.Errorf("error DecodeString %v", err)
					}
					fmt.Println("size of root in bytes ", len(bytes))
					allmsgs = append(allmsgs, bytes...)
				}

				msgdata := &pb.AllMessages{}
				msgdata.Len = uint32(len(roots))
				msgdata.Msgs = allmsgs

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
			allmsgs := make([][]byte, len(peerInfo.DcXorVector))
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
			}
			joinSession.Unlock()
		}
	}
}
