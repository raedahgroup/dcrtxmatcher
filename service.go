package main

import (
	"bytes"

	"google.golang.org/grpc/peer"

	"github.com/decred/dcrd/wire"
	"github.com/raedahgroup/dcrtxmatcher/matcher"
	"golang.org/x/net/context"

	pb "github.com/decred/dcrwallet/dcrtxclient/api/matcherrpc"
)

type SplitTxMatcherService struct {
	//ticketJoiner *matcher.JoinSession

	ticketJoiner *matcher.JoinTicker
	waitingQueue *matcher.WaitingQueue
}

func NewSplitTxMatcherService(ticketJoiner *matcher.JoinTicker, waitingQueue *matcher.WaitingQueue) *SplitTxMatcherService {
	return &SplitTxMatcherService{
		ticketJoiner: ticketJoiner,
		waitingQueue: waitingQueue,
	}
}

func (svc *SplitTxMatcherService) FindMatches(ctx context.Context, req *pb.FindMatchesRequest) (*pb.FindMatchesResponse, error) {

	var sess *matcher.SessionParticipant
	var err error

	sessID := svc.ticketJoiner.NewSessionID()

	peer, ok := peer.FromContext(ctx)

	if !ok {

	}

	log.Infof("SessionID %v - %v connected", sessID, peer.Addr.String())

	done := make(chan bool)

	go func() {
		defer func() {
			done <- true
		}()
		sess, err = svc.waitingQueue.AddParticipant(req.Amount, sessID)

	}()

	for {
		select {
		case <-done:
			if err != nil {
				return nil, err
			}
			res := &pb.FindMatchesResponse{
				SessionId: string(sessID),
				JoinId:    string(sess.JoinSessionID),
			}
			return res, nil
		case <-ctx.Done():
			svc.waitingQueue.RemoveWaitingID(sessID)
			err = ctx.Err()
			return nil, err
		}
	}

}

func (svc *SplitTxMatcherService) SubmitSplitTx(ctx context.Context, req *pb.SubmitInputTxReq) (*pb.SubmitInputTxRes, error) {

	var splitTx *wire.MsgTx
	//decode tx from dcrwallet client
	splitBuff := bytes.NewBuffer(req.GetSplitTx())
	splitTx = wire.NewMsgTx()
	splitTx.BtcDecode(splitBuff, 0)

	var resp *pb.SubmitInputTxRes

	done := make(chan bool)

	var ticket *wire.MsgTx
	var inputIds, outputIds []int32
	var err error

	joinSession := svc.ticketJoiner.GetJoinSession(req.JoinId)
	if joinSession == nil {
		log.Debugf("joinSession is nil")
		//return nil, errors.New("Error joinSession is nil")
	}

	go func() {
		defer func() {
			done <- true
		}()
		ticket, inputIds, outputIds, err = joinSession.SubmitSplitTx(matcher.SessionID(req.SessionId), splitTx,
			int(0), nil)
		if err != nil {
			log.Debugf("matcher.PublishTransaction error %v", err)
			resp = nil
			return
		}
		buff := bytes.NewBuffer(nil)
		buff.Grow(ticket.SerializeSize())
		err = ticket.BtcEncode(buff, 0)
		if err != nil {
			log.Errorf("matcher.BtcEncode error %v ", err)
			resp = nil
			return
		}

		resp = &pb.SubmitInputTxRes{
			TicketTx:  buff.Bytes(),
			InputsIds: inputIds,
			OutputIds: outputIds,
		}
		done <- true
		return
	}()

	for {
		select {
		case <-done:
			return resp, err
		case <-ctx.Done():
			//remove this sessionID
			joinSession.RemoveSessionID(matcher.SessionID(req.SessionId))
			err = ctx.Err()
			return nil, err
		}
	}

}

//each participant invidually send signed inputs.
//when all inputs of all participants are received
//full transaction is built
func (svc *SplitTxMatcherService) SubmitSignedTransaction(ctx context.Context, req *pb.SignTransactionRequest) (*pb.SignTransactionResponse, error) {

	var resp *pb.SignTransactionResponse
	var errn error

	done := make(chan bool)

	joinSession := svc.ticketJoiner.GetJoinSession(req.JoinId)
	if joinSession == nil {
		log.Debugf("joinSession is nil")
		//return nil, errors.New("Error joinSession is nil")
	}

	go func() {
		defer func() {
			done <- true
		}()
		//decode tx from dcrwallet client
		splitBuff := bytes.NewBuffer(req.GetSplitTx())
		tx := wire.NewMsgTx()
		tx.BtcDecode(splitBuff, 0)

		ticket, publisher, err := joinSession.SubmitSignedTx(matcher.SessionID(req.SessionId), tx)
		if err != nil {
			log.Errorf("matcher.SubmitSignedTransaction error %v ", err)
			errn = err
			return
		}

		buff := bytes.NewBuffer(nil)
		buff.Grow(ticket.SerializeSize())
		err = ticket.BtcEncode(buff, 0)
		if err != nil {
			errn = err
			return
		}

		resp = &pb.SignTransactionResponse{
			TicketTx:  buff.Bytes(),
			Publisher: publisher,
		}

	}()

	for {
		select {
		case <-done:
			return resp, errn

		case <-ctx.Done():
			//remove this sessionID
			joinSession.RemoveSessionID(matcher.SessionID(req.SessionId))
			return nil, ctx.Err()
		}
	}
}

func (svc *SplitTxMatcherService) PublishResult(ctx context.Context, req *pb.PublishResultRequest) (*pb.PublishResultResponse, error) {
	var tx, publishedTx *wire.MsgTx
	var err error
	var resp *pb.PublishResultResponse

	done := make(chan bool)
	joinSession := svc.ticketJoiner.GetJoinSession(req.JoinId)
	if joinSession == nil {
		log.Debugf("joinSession is nil")
	}

	go func() {
		defer func() {
			done <- true
		}()
		if req.JoinedTx != nil {
			txResult := bytes.NewBuffer(req.JoinedTx)
			tx = wire.NewMsgTx()
			tx.BtcDecode(txResult, 0)
		}

		publishedTx, err = joinSession.PublishResult(matcher.SessionID(req.SessionId), tx)
		if err != nil {
			log.Debugf("matcher.PublishResult error %v", err)
			return
		}

		buff := bytes.NewBuffer(nil)
		buff.Grow(publishedTx.SerializeSize())
		err = publishedTx.BtcEncode(buff, 0)
		if err != nil {
			return
		}
		resp = &pb.PublishResultResponse{
			TicketTx: buff.Bytes(),
		}
	}()

	for {
		select {
		case <-done:
			return resp, err

		case <-ctx.Done():
			joinSession.RemoveSessionID(matcher.SessionID(req.SessionId))
			return nil, ctx.Err()
		}
	}
}

func (svc *SplitTxMatcherService) Status(context.Context, *pb.StatusRequest) (*pb.StatusResponse, error) {
	return &pb.StatusResponse{
		TicketPrice: 666,
	}, nil
}
