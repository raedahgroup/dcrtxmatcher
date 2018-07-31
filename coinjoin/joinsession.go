package coinjoin

import (
	"sync"

	"github.com/gogo/protobuf/proto"

	pb "github.com/raedahgroup/dcrtxmatcher/api/messages"
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
		//		expDCVectorChan    chan pb.KeyExchangeReq
		//		simpleDCVectorChan chan pb.KeyExchangeReq
	}
)

func NewJoinSession(sessionId uint32) *JoinSession {
	return &JoinSession{
		Id:              sessionId,
		Peers:           make(map[uint32]*PeerInfo),
		keyExchangeChan: make(chan pb.KeyExchangeReq),
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
					joinSession.PKs = append(joinSession.PKs, &pb.PeerInfo{PeerId: peer.Id, Pk: peer.PK})
				}

				//if there is enough pk, broadcast pks to all clients
				if len(joinSession.Peers) == len(joinSession.PKs) {
					keyex := &pb.KeyExchangeRes{
						Peers: joinSession.PKs,
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
			//	case <-joinSession.expDCVectorChan:
			//	case <-joinSession.simpleDCVectorChan:

		}
	}
}
