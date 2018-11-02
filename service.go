package main

import (
	"bytes"
	"errors"
	"fmt"

	"google.golang.org/grpc/peer"

	"github.com/decred/dcrd/wire"
	"github.com/raedahgroup/dcrtxmatcher/matcher"
	"golang.org/x/net/context"

	pb "github.com/decred/dcrwallet/dcrtxclient/api/matcherrpc"
)

type SplitTxMatcherService struct {
	ticketJoiner *matcher.JoinTicker
	joinQueue    *matcher.JoinQueue
}

// NewSplitTxMatcherService creates new server matcher engine.
func NewSplitTxMatcherService(ticketJoiner *matcher.JoinTicker, joinQueue *matcher.JoinQueue) *SplitTxMatcherService {
	return &SplitTxMatcherService{
		ticketJoiner: ticketJoiner,
		joinQueue:    joinQueue,
	}
}

// FindMatches sends join session matcher request to server,
// waits for enough participants join and then returns the session id of join session.
func (svc *SplitTxMatcherService) FindMatches(ctx context.Context, req *pb.FindMatchesRequest) (*pb.FindMatchesResponse, error) {
	var sess *matcher.SessionParticipant
	var err error
	sessID := svc.ticketJoiner.NewSessionID()

	// Get meta data of peer from context.
	// We need only the ip address.
	clientInfo, _ := peer.FromContext(ctx)
	log.Infof("SessionID %v - %v connected", sessID, clientInfo.Addr.String())

	done := make(chan bool)

	// AddParticipant is long running function.
	// So we use goroutine to not blocking cancel signal on context.
	go func() {
		sess, err = svc.joinQueue.AddParticipant(req.Amount, sessID)
		done <- true
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
			svc.joinQueue.RemoveWaitingID(sessID)
			err = ctx.Err()
			done <- true
			return nil, err
		}
	}

}

// SubmitSplitTx receives transaction from one participant
// and merges with other's transaction.
// Returns input, output index of each participant and the merged transaction.
func (svc *SplitTxMatcherService) SubmitSplitTx(ctx context.Context, req *pb.SubmitInputTxReq) (*pb.SubmitInputTxRes, error) {
	var splitTx *wire.MsgTx
	// Decode transaction from dcrwallet client
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
		return nil, errors.New(fmt.Sprintf("Can not find joinSession with id %d", req.JoinId))
	}
	// SubmitSplitTx is long running function.
	// So we use goroutine to not blocking cancel signal on context.
	go func() {
		ticket, inputIds, outputIds, err = joinSession.SubmitSplitTx(matcher.SessionID(req.SessionId), splitTx,
			int(0), nil)
		if err != nil {
			log.Debugf("Can not submit join transaction: %v", err)
			resp = nil
			return
		}
		buff := bytes.NewBuffer(nil)
		buff.Grow(ticket.SerializeSize())
		err = ticket.BtcEncode(buff, 0)
		if err != nil {
			log.Errorf("Can not encode buffer transaction: %v ", err)
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
			// Remove participant with the session id.
			joinSession.RemoveSessionID(matcher.SessionID(req.SessionId))
			err = ctx.Err()
			done <- true
			return nil, err
		}
	}

}

// SubmitSignedTransaction sends signed inputs to server.
// When all inputs of all participants are received,
// full transaction is built.
func (svc *SplitTxMatcherService) SubmitSignedTransaction(ctx context.Context, req *pb.SignTransactionRequest) (*pb.SignTransactionResponse, error) {
	var resp *pb.SignTransactionResponse
	var errn error

	done := make(chan bool)
	joinSession := svc.ticketJoiner.GetJoinSession(req.JoinId)
	if joinSession == nil {
		return nil, errors.New(fmt.Sprintf("Can not find join session with id %d", req.JoinId))
	}
	// SubmitSignedTx is long running function.
	// So we use goroutine to not blocking cancel signal on context.
	go func() {
		// Decode transaction from dcrwallet client
		splitBuff := bytes.NewBuffer(req.GetSplitTx())
		tx := wire.NewMsgTx()
		tx.BtcDecode(splitBuff, 0)

		ticket, publisher, err := joinSession.SubmitSignedTx(matcher.SessionID(req.SessionId), tx)
		if err != nil {
			log.Errorf("Can not submit signed transaction: %v ", err)
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
		done <- true
	}()

	for {
		select {
		case <-done:
			return resp, errn

		case <-ctx.Done():
			// Remove participant with the session id.
			joinSession.RemoveSessionID(matcher.SessionID(req.SessionId))
			done <- true
			return nil, ctx.Err()
		}
	}
}

// PublishResult receives the published transaction data
// from one participant and forward the transaction to others.
func (svc *SplitTxMatcherService) PublishResult(ctx context.Context, req *pb.PublishResultRequest) (*pb.PublishResultResponse, error) {
	var tx, publishedTx *wire.MsgTx
	var err error
	var resp *pb.PublishResultResponse

	done := make(chan bool)
	joinSession := svc.ticketJoiner.GetJoinSession(req.JoinId)
	if joinSession == nil {
		return nil, errors.New(fmt.Sprintf("Can not find join session with id %d", req.JoinId))
	}

	go func() {
		if req.JoinedTx != nil {
			txResult := bytes.NewBuffer(req.JoinedTx)
			tx = wire.NewMsgTx()
			tx.BtcDecode(txResult, 0)
		}

		publishedTx, err = joinSession.PublishResult(matcher.SessionID(req.SessionId), tx)
		if err != nil {
			log.Debugf("Can not execute publish result: %v", err)
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
		done <- true
	}()

	for {
		select {
		case <-done:
			return resp, err

		case <-ctx.Done():
			joinSession.RemoveSessionID(matcher.SessionID(req.SessionId))
			done <- true
			return nil, ctx.Err()
		}
	}
}
