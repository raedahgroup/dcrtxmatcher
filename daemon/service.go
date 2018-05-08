package daemon

import (
	"bytes"

	//"github.com/decred/dcrd/dcrutil"
	"github.com/decred/dcrd/wire"
	"github.com/raedahgroup/dcrtxmatcher/matcher"
	"golang.org/x/net/context"

	"fmt"

	pb "github.com/raedahgroup/dcrtxmatcher/api/matcherrpc"
)

type SplitTxMatcherService struct {
	matcher *matcher.Matcher
}

func NewSplitTxMatcherService(matcher *matcher.Matcher) *SplitTxMatcherService {
	return &SplitTxMatcherService{
		matcher: matcher,
	}
}

func (svc *SplitTxMatcherService) FindMatches(ctx context.Context, req *pb.FindMatchesRequest) (*pb.FindMatchesResponse, error) {

	sess, err := svc.matcher.AddParticipant(req.Amount)
	if err != nil {
		return nil, err
	}

	res := &pb.FindMatchesResponse{
		SessionId: int32(sess.ID),
	}

	return res, nil
}

func (svc *SplitTxMatcherService) PublishTicket(ctx context.Context, req *pb.SubmitInputTxReq) (*pb.SubmitInputTxRes, error) {

	var splitTx *wire.MsgTx
	//decode tx from dcrwallet client
	splitBuff := bytes.NewBuffer(req.GetSplitTx())
	splitTx = wire.NewMsgTx()
	splitTx.BtcDecode(splitBuff, 0)

	ticket, inputIds, outputIds, err := svc.matcher.PublishTransaction(matcher.SessionID(req.SessionId), splitTx,
		int(0), nil)
	if err != nil {
		fmt.Println("matcher.PublishTransaction error ", err)
		return nil, err
	}

	buff := bytes.NewBuffer(nil)
	buff.Grow(ticket.SerializeSize())
	err = ticket.BtcEncode(buff, 0)
	if err != nil {
		log.Errorf("matcher.BtcEncode error %v ", err)
		return nil, err
	}
	fmt.Println("PublishTicketResponse sent")
	resp := &pb.SubmitInputTxRes{
		TicketTx:  buff.Bytes(),
		InputsIds: inputIds,
		OutputIds: outputIds,
	}
	return resp, nil
}

//each participant invidually send signed inputs.
//when all inputs of all participants are received
//full transaction is built
func (svc *SplitTxMatcherService) SubmitSignedTransaction(ctx context.Context, req *pb.SignTransactionRequest) (*pb.SignTransactionResponse, error) {

	//decode tx from dcrwallet client
	splitBuff := bytes.NewBuffer(req.GetSplitTx())
	tx := wire.NewMsgTx()
	tx.BtcDecode(splitBuff, 0)

	ticket, publisher, err := svc.matcher.SubmitSignedTransaction(matcher.SessionID(req.SessionId), tx)
	if err != nil {
		log.Errorf("matcher.SubmitSignedTransaction error %v ", err)
		return nil, err
	}

	buff := bytes.NewBuffer(nil)
	buff.Grow(ticket.SerializeSize())
	err = ticket.BtcEncode(buff, 0)
	if err != nil {
		return nil, err
	}

	resp := &pb.SignTransactionResponse{
		TicketTx:  buff.Bytes(),
		Publisher: publisher,
	}
	return resp, nil
}

func (svc *SplitTxMatcherService) PublishResult(ctx context.Context, req *pb.PublishResultRequest) (*pb.PublishResultResponse, error) {
	var tx *wire.MsgTx
	if req.JoinedTx != nil {
		txResult := bytes.NewBuffer(req.JoinedTx)
		tx = wire.NewMsgTx()
		tx.BtcDecode(txResult, 0)
	}

	publishedTx, err := svc.matcher.PublishResult(matcher.SessionID(req.SessionId), tx)
	if err != nil {
		fmt.Println("matcher.BtcEncode error ", err)
		return nil, err
	}

	buff := bytes.NewBuffer(nil)
	buff.Grow(publishedTx.SerializeSize())
	err = publishedTx.BtcEncode(buff, 0)
	if err != nil {
		return nil, err
	}
	resp := &pb.PublishResultResponse{
		TicketTx: buff.Bytes(),
	}
	return resp, nil

}

func (svc *SplitTxMatcherService) Status(context.Context, *pb.StatusRequest) (*pb.StatusResponse, error) {
	return &pb.StatusResponse{
		TicketPrice: 666,
	}, nil
}
