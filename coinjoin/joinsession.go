package coinjoin

import (
	"fmt"
	"sync"

	"github.com/gogo/protobuf/proto"

	pb "github.com/raedahgroup/dcrtxmatcher/api/messages"
	"github.com/raedahgroup/dcrtxmatcher/finitefield"
	"github.com/raedahgroup/dcrtxmatcher/flint"
)

type (
	JoinSession struct {
		Id uint32
		sync.Mutex
		Peers   map[uint32]*PeerInfo
		DiceMix *DiceMix
		State   int

		PKs []*pb.PeerInfo

		keyExchangeChan    chan pb.KeyExchangeReq
		dcExpVectorChan    chan pb.DcExpVector
		dcSimpleVectorChan chan pb.DcSimpleVector
	}
)

func NewJoinSession(sessionId uint32) *JoinSession {
	return &JoinSession{
		Id:                 sessionId,
		Peers:              make(map[uint32]*PeerInfo),
		keyExchangeChan:    make(chan pb.KeyExchangeReq),
		dcExpVectorChan:    make(chan pb.DcExpVector),
		dcSimpleVectorChan: make(chan pb.DcSimpleVector),
		PKs:                make([]*pb.PeerInfo, 0),
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
			fmt.Println("dcExpVectorChan data.Vector", data.Vector)
			vector := make([]field.Field, 0)
			for i := 0; i < int(data.Len); i++ {
				b := data.Vector[i*16 : (i+1)*16]
				ff := field.NewFF(field.FromBytes(b))
				vector = append(vector, ff)
			}

			peerInfo.DcExpVector = vector
			peerInfo.Commit = data.Commit

			fmt.Printf("commit %x", peerInfo.Commit)
			fmt.Printf("vector size %x", len(vector))

			allSubmit := true
			for _, peer := range joinSession.Peers {
				if len(peer.DcExpVector) == 0 {
					allSubmit = false
				}
			}

			//solve polynomial to get roots
			poly_degree := len(peerInfo.DcExpVector)
			dc_combine := make([]field.Field, len(joinSession.Peers))
			if allSubmit {
				for _, peer := range joinSession.Peers {
					for i := 0; i < len(peer.DcExpVector); i++ {
						dc_combine[i] = dc_combine[i].Add(peer.DcExpVector[i])
					}

				}
			}

			ret, roots := flint.GetRoots(field.Prime.HexStr(), dc_combine, poly_degree)
			fmt.Printf("ret %d. number roots: %d, roots: %v\n", ret, len(roots), roots)
			joinSession.Unlock()

			//	case <-joinSession.simpleDCVectorChan:

		}
	}
}
