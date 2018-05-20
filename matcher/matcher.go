package matcher

import (
	"errors"
	"math/rand"
	"time"

	"github.com/decred/dcrd/wire"
	"github.com/xid"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

type (
	// SessionID stores the unique id for an in-progress join split tx session
	SessionID string

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

		chanPublishTicketResponse  chan submitSplitTxRes
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
		//waitingParticipants []*addParticipantRequest
		waitingParticipants map[SessionID]*addParticipantRequest
		sessions            map[SessionID]*SessionParticipant
		cfg                 *Config

		addParticipantRequests chan addParticipantRequest
		publishTicketRequests  chan submitSplitTxReq
		submitSignedTxRequest  chan submitSignedTxRequest
		publishedTxReq         chan publishedTxReq
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
		waitingParticipants:    make(map[SessionID]*addParticipantRequest),
		addParticipantRequests: make(chan addParticipantRequest),
		publishTicketRequests:  make(chan submitSplitTxReq),
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
			matcher.waitingParticipants[req.sessID] = &req
			if matcher.enoughForMergeTx() {
				matcher.startNewMergeSession()
				log.Info("Enough participants for start new session")
			}
		case req := <-matcher.publishTicketRequests:
			//check whether any participant has left session
			if len(matcher.sessions) < matcher.cfg.MaxParticipants {
				req.resp <- submitSplitTxRes{
					err: ErrParticipantLeft,
				}
			}
			_, _, err := matcher.addParticipantInput(&req)
			if err != nil {
				req.resp <- submitSplitTxRes{
					err: err,
				}
			}
		case req := <-matcher.submitSignedTxRequest:
			//check whether any participant has left session
			if len(matcher.sessions) < matcher.cfg.MaxParticipants {
				req.resp <- submitSignedTxResponse{
					err: ErrParticipantLeft,
				}
			}

			err := matcher.mergeSignedInput(&req)
			if err != nil {
				req.resp <- submitSignedTxResponse{
					err: err,
				}
			}
		case req := <-matcher.publishedTxReq:
			//check whether any participant has left session
			if len(matcher.sessions) < matcher.cfg.MaxParticipants {
				req.resp <- publishedTxRes{
					err: ErrParticipantLeft,
				}
			}

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
	i := 0
	for _, r := range matcher.waitingParticipants {
		//id := matcher.newSessionID()
		sessPart := &SessionParticipant{
			Session: sess,
			Index:   i,
			ID:      r.sessID,
		}
		sess.Participants[i] = sessPart
		i++
		matcher.sessions[r.sessID] = sessPart
		r.resp <- addParticipantResponse{
			participant: sessPart,
		}
	}

	matcher.waitingParticipants = make(map[SessionID]*addParticipantRequest)
}

func (matcher *Matcher) NewSessionID() SessionID {
	id := xid.New()
	return SessionID(id.String())
}

func (matcher *Matcher) mergeSignedInput(req *submitSignedTxRequest) error {

	if _, has := matcher.sessions[req.sessionID]; !has {
		return ErrSessionNotFound
	}
	participant := matcher.sessions[req.sessionID]
	participant.SignedTx = req.tx

	participant.chanSubmitSignedTxResponse = req.resp

	//if this is first participant session then assign splittx to MergedSplitTx
	if participant.Session.SignedTx == nil {
		participant.Session.SignedTx = req.tx.Copy()
	}

	//needs to random txout here

	for _, i := range participant.InputIndes {
		if participant.SignedTx.TxIn[i] == nil {
			return errors.New("Submit invalid(nil) input")
		}
		participant.Session.SignedTx.TxIn[i] = participant.SignedTx.TxIn[i]
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
		}
	}
	return nil

}

func (matcher *Matcher) RemoveSessionID(sessionID SessionID) error {
	_, ok := matcher.sessions[sessionID]
	if ok {
		delete(matcher.sessions, sessionID)
		log.Infof("Participant left, removed sessionID %v", sessionID)
		return nil
	}
	return errors.New("SessionID does not exist")
}

func (matcher *Matcher) RemoveWaitingSessionID(sessionID SessionID) error {
	_, ok := matcher.waitingParticipants[sessionID]
	if ok {
		delete(matcher.waitingParticipants, sessionID)
		log.Infof("Participant disconnected. Removed sessionID %v from waiting queue", sessionID)
		return nil
	}
	return errors.New("SessionID does not exist")
}

func (matcher *Matcher) addParticipantInput(req *submitSplitTxReq) ([]int32, []int32, error) {

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

	if participant.Session.SubTxAdded() {
		log.Info("All participants submitted the split transaction, Sending merged split transactions back for each participant")
		for _, p := range participant.Session.Participants {
			p.chanPublishTicketResponse <- submitSplitTxRes{
				err:         nil,
				tx:          participant.Session.MergedSplitTx,
				inputIndes:  p.InputIndes,
				outputIndes: p.OutputIndes,
			}
			//			for _, txin := range p.Session.MergedSplitTx.TxIn {
			//				log.Debug("MergedSplitTx.TxIn ", txin.PreviousOutPoint.Hash, txin.PreviousOutPoint.Index)
			//			}
			//			for _, txout := range p.Session.MergedSplitTx.TxOut {
			//				log.Debug("MergedSplitTx.TxOut ", txout.Version, txout.Value)
			//			}
		}
	}
	return inputIndes, outputIndes, nil
}

func (matcher *Matcher) AddParticipant(maxAmount uint64, sessID SessionID) (*SessionParticipant, error) {

	if len(matcher.waitingParticipants) >= matcher.cfg.MaxParticipants {
		return nil, ErrTooManyParticipants
	}

	req := addParticipantRequest{
		maxAmount: maxAmount,
		sessID:    sessID,
		resp:      make(chan addParticipantResponse),
	}
	matcher.addParticipantRequests <- req

	resp := <-req.resp
	return resp.participant, resp.err
}

// PublishTransaction validates the signed input provided by one of the
// participants of the given session and publishes the transaction. It blocks
// until all participants have sent their inputs
func (matcher *Matcher) SubmitSplitTx(sessionID SessionID, splitTx *wire.MsgTx,
	splitTxOutputIndex int, input *wire.TxIn) (*wire.MsgTx, []int32, []int32, error) {

	req := submitSplitTxReq{
		sessionID:          sessionID,
		splitTx:            splitTx,
		input:              input,
		splitTxOutputIndex: splitTxOutputIndex,
		resp:               make(chan submitSplitTxRes),
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
