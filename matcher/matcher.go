package matcher

import (
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/decred/dcrd/wire"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

type (
	// SessionID stores the unique id for an in-progress ticket buying session
	SessionID int32

	// SessionParticipant is a participant of a split in a given session
	SessionParticipant struct {
		ID       SessionID
		SplitTx  *wire.MsgTx
		SignedTx *wire.MsgTx
		Session  *Session
		Index    int
		//add new
		InputIndes    []int32
		OutputIndes   []int32
		Publisher     bool
		SentPublished bool

		chanPublishTicketResponse  chan publishTicketResponse
		chanSubmitSignedTxResponse chan submitSignedTxResponse
		chanPublishedTxRes         chan publishedTxRes
	}

	// Session is a particular ticket being built
	Session struct {
		Participants []*SessionParticipant

		MergedSplitTx *wire.MsgTx
		SignedTx      *wire.MsgTx
		PublishedTx   *wire.MsgTx
		PublishIndex  int
	}
	// Config stores the parameters for the matcher engine
	Config struct {
		MaxParticipants int
	}

	// Matcher is the main engine for matching operations
	Matcher struct {
		waitingParticipants []*addParticipantRequest
		sessions            map[SessionID]*SessionParticipant
		cfg                 *Config
		//log                 *logging.Logger

		addParticipantRequests chan addParticipantRequest
		publishTicketRequests  chan publishTicketRequest
		submitSignedTxRequest  chan submitSignedTxRequest
		publishedTxReq         chan publishedTxReq
	}

	TicketPriceProvider interface {
		CurrentTicketPrice() uint64
	}
)

// InputsSigned returns true if all participant already sent their signed input/output
func (sess *Session) InputsSigned() bool {
	for _, p := range sess.Participants {
		if p.SignedTx == nil {
			return false
		}
	}
	return true
}

//AllSentPublished returns true if all participant already sent their publish result
func (sess *Session) AllSentPublished() bool {
	for _, p := range sess.Participants {
		if !p.SentPublished {
			return false
		}
	}
	return true
}

// SubTxAdded returns true if all participant already sent their invidual input/output tx
func (sess *Session) SubTxAdded() bool {
	for _, p := range sess.Participants {
		if p.SplitTx == nil {
			return false
		}
	}
	return true
}

func NewMatcher(cfg *Config) *Matcher {
	m := &Matcher{
		cfg:                    cfg,
		sessions:               make(map[SessionID]*SessionParticipant),
		addParticipantRequests: make(chan addParticipantRequest),
		publishTicketRequests:  make(chan publishTicketRequest),
		submitSignedTxRequest:  make(chan submitSignedTxRequest),
		publishedTxReq:         make(chan publishedTxReq),
	}

	m.addParticipantRequests = make(chan addParticipantRequest, cfg.MaxParticipants)
	return m
}

// Run listens for all matcher messages and runs the matching engine.
func (matcher *Matcher) Run() error {
	for {
		select {
		case req := <-matcher.addParticipantRequests:
			matcher.waitingParticipants = append(matcher.waitingParticipants, &req)
			if matcher.enoughForMergeTx() {
				matcher.startNewMergeSession()
				log.Debug("matcher.enoughForMergeTx start newMergeSession")
			}
		case req := <-matcher.publishTicketRequests:
			_, _, err := matcher.addParticipantInput(&req)
			if err != nil {
				req.resp <- publishTicketResponse{
					err: err,
				}
			}
		case req := <-matcher.submitSignedTxRequest:
			err := matcher.mergeSignedInput(&req)
			if err != nil {
				req.resp <- submitSignedTxResponse{
					err: err,
				}
			}
		case req := <-matcher.publishedTxReq:
			err := matcher.publishTxResult(&req)
			if err != nil {
				req.resp <- publishedTxRes{
					err: err,
				}
			}
		}
	}
}

//check wether enough users to start merge splittx
func (matcher *Matcher) enoughForMergeTx() bool {
	return len(matcher.waitingParticipants) == matcher.cfg.MaxParticipants
}

func (matcher *Matcher) startNewMergeSession() {
	sessSize := len(matcher.waitingParticipants)

	sess := &Session{
		Participants: make([]*SessionParticipant, sessSize),
	}
	for i, r := range matcher.waitingParticipants {
		id := matcher.newSessionID()
		fmt.Println("startNewMergeSession - session ID ", id)
		sessPart := &SessionParticipant{
			Session: sess,
			Index:   i,
			ID:      id,
		}

		sess.Participants[i] = sessPart
		matcher.sessions[id] = sessPart
		r.resp <- addParticipantResponse{
			participant: sessPart,
		}
	}

	matcher.waitingParticipants = nil
}

//func (matcher *Matcher) enoughForNewSession(ticketPrice dcrutil.Amount) bool {
//
//	var availableSum uint64
//
//	for _, r := range matcher.waitingParticipants {
//		availableSum += r.maxAmount
//	}
//
//	ticketFee := SessionFeeEstimate(len(matcher.waitingParticipants))
//	neededAmount := uint64(ticketPrice + ticketFee)
//	return availableSum > neededAmount
//}

//func (matcher *Matcher) startNewSession(ticketPrice uint64) {
//	numParts := len(matcher.waitingParticipants)
//	//ticketFee := SessionFeeEstimate(numParts)
//	//partFee := dcrutil.Amount(math.Ceil(float64(ticketFee) / float64(numParts)))
//
//
//
//
//
//	sess := &Session{
//		Participants: make([]*SessionParticipant, numParts),
//		//TicketPrice:  dcrutil.Amount(matcher.cfg.PriceProvider.CurrentTicketPrice()),
//		//TicketPrice:  dcrutil.Amount(ticketPrice),
//		//VoterIndex:   0, // FIXME: select voter index at random
//	}
//
//	amountLeft := dcrutil.Amount(ticketPrice)
//	for i, r := range matcher.waitingParticipants {
//		amount := dcrutil.Amount(r.maxAmount) - partFee
//		if amount > amountLeft {
//			amount = amountLeft
//		}
//
//		sessPart := &SessionParticipant{
//			//Amount:  amount,
//			//Fee:     partFee,
//			Session: sess,
//			Index:   i,
//		}
//		matcher.log.Infof("SessionParticipant: Amount=%s partFee=%s Index=%d",
//			amount,
//			partFee, i)
//
//		sess.Participants[i] = sessPart
//
//		id := matcher.newSessionID()
//		matcher.sessions[id] = sessPart
//		sessPart.ID = id
//
//		r.resp <- addParticipantResponse{
//			participant: sessPart,
//		}
//
//		amountLeft -= amount
//	}
//
//	matcher.waitingParticipants = nil
//}

func (matcher *Matcher) newSessionID() SessionID {
	// TODO: rw lock matcher.sessions here
	id := MustRandInt32()
	for _, has := matcher.sessions[SessionID(id)]; has; {
		id = MustRandInt32()
	}
	return SessionID(id)
}

func (matcher *Matcher) mergeSignedInput(req *submitSignedTxRequest) error {

	if _, has := matcher.sessions[req.sessionID]; !has {
		return ErrSessionNotFound
	}
	participant := matcher.sessions[req.sessionID]
	participant.SignedTx = req.tx

	//if len(participant.SignedTx.TxIn) != len(participant.InputIndes){
	//	return ErrSessionNotFound
	//}

	participant.chanSubmitSignedTxResponse = req.resp

	//if this is first participant session then assign splittx to MergedSplitTx
	if participant.Session.SignedTx == nil {
		fmt.Println("participant.Session.SignedTx == nil ")
		participant.Session.SignedTx = req.tx.Copy()
	}

	for _, i := range participant.InputIndes {
		if participant.SignedTx.TxIn[i] == nil {
			return errors.New("Submit invalid(nil) input")
		}
		participant.Session.SignedTx.TxIn[i] = participant.SignedTx.TxIn[i]
		fmt.Println("index , script ", i, participant.Session.SignedTx.TxIn[i].SignatureScript)
	}
	for _, i := range participant.OutputIndes {
		if participant.SignedTx.TxOut[i] == nil {
			return errors.New("Submit invalid(nil) input")
		}
		participant.Session.SignedTx.TxOut[i] = participant.SignedTx.TxOut[i]
	}

	if participant.Session.InputsSigned() {
		//get random parti publish transaction
		randIndex := rand.Intn(len(participant.Session.Participants))

		for i, p := range participant.Session.Participants {
			publisher := false
			if i == randIndex {
				publisher = true
				fmt.Println("publisher randIndex", randIndex)
				p.Publisher = publisher
			}
			p.chanSubmitSignedTxResponse <- submitSignedTxResponse{
				err:       nil,
				tx:        participant.Session.SignedTx,
				publisher: publisher,
			}

		}
	}
	return nil
}

func (matcher *Matcher) publishTxResult(req *publishedTxReq) error {
	if _, has := matcher.sessions[req.sessionID]; !has {
		return ErrSessionNotFound
	}
	participant := matcher.sessions[req.sessionID]

	participant.SentPublished = true
	participant.chanPublishedTxRes = req.resp
	if req.tx != nil && participant.Publisher {
		participant.Session.PublishedTx = req.tx
	}

	if participant.Session.AllSentPublished() {
		for _, p := range participant.Session.Participants {
			p.chanPublishedTxRes <- publishedTxRes{
				tx:  req.tx,
				err: nil,
			}
			fmt.Println("publishTxResult ID ", p.ID)
		}
	}
	return nil

}

func (matcher *Matcher) addParticipantInput(req *publishTicketRequest) ([]int32, []int32, error) {

	if _, has := matcher.sessions[req.sessionID]; !has {
		return nil, nil, ErrSessionNotFound
	}

	inputIndes := make([]int32, 0)
	outputIndes := make([]int32, 0)

	participant := matcher.sessions[req.sessionID]
	participant.SplitTx = req.splitTx

	participant.chanPublishTicketResponse = req.resp

	//if this is first participant session then assign splittx to MergedSplitTx
	if participant.Session.MergedSplitTx == nil {
		participant.Session.MergedSplitTx = req.splitTx.Copy()

		for i, _ := range participant.Session.MergedSplitTx.TxIn {
			inputIndes = append(inputIndes, int32(i))
		}
		for i, _ := range participant.Session.MergedSplitTx.TxOut {
			outputIndes = append(outputIndes, int32(i))
		}
	} else {
		k := 0
		inputSize := len(participant.Session.MergedSplitTx.TxIn)
		for _, txin := range req.splitTx.TxIn {
			participant.Session.MergedSplitTx.AddTxIn(txin)
			inputIndes = append(inputIndes, int32(inputSize+k))
			k++
		}
		k = 0
		outputSize := len(participant.Session.MergedSplitTx.TxOut)
		for _, txout := range req.splitTx.TxOut {
			participant.Session.MergedSplitTx.AddTxOut(txout)
			outputIndes = append(outputIndes, int32(outputSize+k))
			k++
		}
	}

	participant.InputIndes = inputIndes
	participant.OutputIndes = outputIndes
	fmt.Println("Setting input for participant ", req.sessionID, inputIndes, outputIndes)
	fmt.Println("MergedSplitTx. txin, txout", len(participant.Session.MergedSplitTx.TxIn), len(participant.Session.MergedSplitTx.TxOut))

	if participant.Session.SubTxAdded() {
		fmt.Println("[PublishTx]-All SubTxAdded are added for session")
		for _, p := range participant.Session.Participants {
			fmt.Println("[PublishTx]-All SubTxAdded info", participant.InputIndes, participant.ID)
			p.chanPublishTicketResponse <- publishTicketResponse{
				err:         nil,
				tx:          participant.Session.MergedSplitTx,
				inputIndes:  p.InputIndes,
				outputIndes: p.OutputIndes,
			}
			for _, txin := range p.Session.MergedSplitTx.TxIn {
				fmt.Println("MergedSplitTx.TxIn ", txin.PreviousOutPoint.Hash, txin.PreviousOutPoint.Index)
			}
			for _, txout := range p.Session.MergedSplitTx.TxOut {
				fmt.Println("MergedSplitTx.TxOut ", txout.Version, txout.Value)
			}
		}
	}
	return inputIndes, outputIndes, nil
}

func (matcher *Matcher) AddParticipant(maxAmount uint64) (*SessionParticipant, error) {

	if len(matcher.waitingParticipants) >= matcher.cfg.MaxParticipants {
		return nil, ErrTooManyParticipants
	}

	req := addParticipantRequest{
		maxAmount: maxAmount,
		resp:      make(chan addParticipantResponse),
	}
	matcher.addParticipantRequests <- req

	resp := <-req.resp
	return resp.participant, resp.err
}

// PublishTransaction validates the signed input provided by one of the
// participants of the given session and publishes the transaction. It blocks
// until all participants have sent their inputs
func (matcher *Matcher) PublishTransaction(sessionID SessionID, splitTx *wire.MsgTx,
	splitTxOutputIndex int, input *wire.TxIn) (*wire.MsgTx, []int32, []int32, error) {

	req := publishTicketRequest{
		sessionID:          sessionID,
		splitTx:            splitTx,
		input:              input,
		splitTxOutputIndex: splitTxOutputIndex,
		resp:               make(chan publishTicketResponse),
	}

	matcher.publishTicketRequests <- req
	resp := <-req.resp
	return resp.tx, resp.inputIndes, resp.outputIndes, resp.err
}

// SubmitSignedTransaction get signed inputs from parti and
// merge to one transaction
func (matcher *Matcher) SubmitSignedTransaction(sessionID SessionID, signedTx *wire.MsgTx) (*wire.MsgTx, bool, error) {

	req := submitSignedTxRequest{
		sessionID: sessionID,
		tx:        signedTx,
		resp:      make(chan submitSignedTxResponse),
	}

	matcher.submitSignedTxRequest <- req
	resp := <-req.resp
	return resp.tx, resp.publisher, resp.err
}

// PublishResult get signed inputs from participant and
// get the published one and send for other participants
func (matcher *Matcher) PublishResult(sessionID SessionID, signedTx *wire.MsgTx) (*wire.MsgTx, error) {

	req := publishedTxReq{
		sessionID: sessionID,
		tx:        signedTx,
		resp:      make(chan publishedTxRes),
	}

	matcher.publishedTxReq <- req
	resp := <-req.resp
	return resp.tx, resp.err
}
