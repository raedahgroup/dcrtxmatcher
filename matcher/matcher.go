package matcher

import (
	"errors"
	"math/rand"
	"time"

	"github.com/decred/dcrd/wire"
	"github.com/rs/xid"
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

		chanSubmitSplitTxRes  chan submitSplitTxRes
		chanSubmitSignedTxRes chan submitSignedTxRes
		chanPublishedTxRes    chan publishedTxRes
	}

	// Session is a particular split tx being built
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
		RandomIndex     bool
		JoinTicker      int
		WaitingTimer    int
	}

	// Matcher is the main engine for matching operations
	Matcher struct {
		//waitingParticipants []*addParticipantRequest
		cfg                 *Config
		waitingParticipants map[SessionID]*addParticipantRequest
		sessions            map[SessionID]*SessionParticipant
		SessionData         *Session

		addParticipantRequests chan addParticipantRequest
		submitSplitTxRequest   chan submitSplitTxReq
		submitSignedTxRequest  chan submitSignedTxRequest
		publishedTxReq         chan publishedTxReq

		sessionTicker *time.Ticker
		waitingTimer  *time.Timer
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
		submitSplitTxRequest:   make(chan submitSplitTxReq),
		submitSignedTxRequest:  make(chan submitSignedTxRequest),
		publishedTxReq:         make(chan publishedTxReq),
		sessionTicker:          time.NewTicker(time.Second * time.Duration(cfg.JoinTicker)),
		waitingTimer:           time.NewTimer(time.Second * time.Duration(cfg.JoinTicker)),
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
			}
		case <-matcher.sessionTicker.C:
			matcher.startNewMergeSession()
		case <-matcher.waitingTimer.C:
			//log.Info("Waiting timer reached")
			//check participants who missing inputs and remove
			missedP := make([]SessionID, 0)
			for sid, p := range matcher.sessions {
				if p.SplitTx == nil {
					missedP = append(missedP, sid)
				}
			}

			for _, sid := range missedP {
				log.Infof("Timeout. Remove sessionID %v from Session", sid)
				delete(matcher.sessions, sid)
			}

		case req := <-matcher.submitSplitTxRequest:

			//log.Info("matcher.submitSplitTxRequest")
			_, _, err := matcher.addParticipantInput(&req)
			if err != nil {
				req.resp <- submitSplitTxRes{
					err: err,
				}
			}
		case req := <-matcher.submitSignedTxRequest:

			err := matcher.mergeSignedInput(&req)
			if err != nil {
				req.resp <- submitSignedTxRes{
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
	if sessSize == 0 {
		return
	}

	log.Info("Start join split transaction")
	matcher.SessionData = &Session{
		Participants: make([]*SessionParticipant, sessSize),
	}

	i := 0
	for _, r := range matcher.waitingParticipants {
		sessPart := &SessionParticipant{
			Session: matcher.SessionData,
			Index:   i,
			ID:      r.sessID,
		}
		matcher.SessionData.Participants[i] = sessPart
		i++
		matcher.sessions[r.sessID] = sessPart
		r.resp <- addParticipantResponse{
			participant: sessPart,
		}
	}
	//add timer for waiting participants send inputs
	matcher.waitingTimer = time.NewTimer(time.Second * time.Duration(matcher.cfg.WaitingTimer))

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

	participant.chanSubmitSignedTxRes = req.resp

	//if this is first participant session then assign splittx to MergedSplitTx
	if matcher.SessionData.SignedTx == nil {
		matcher.SessionData.SignedTx = req.tx.Copy()
	}

	for _, i := range participant.InputIndes {
		if participant.SignedTx.TxIn[i] == nil {
			return errors.New("Submit invalid(nil) input")
		}
		matcher.SessionData.SignedTx.TxIn[i] = participant.SignedTx.TxIn[i]
	}
	for _, i := range participant.OutputIndes {
		if participant.SignedTx.TxOut[i] == nil {
			return errors.New("Submit invalid(nil) input")
		}
		matcher.SessionData.SignedTx.TxOut[i] = participant.SignedTx.TxOut[i]
	}

	if matcher.SessionData.InputsSigned() {
		//select random participant to publish transaction
		randIndex := rand.Intn(len(matcher.SessionData.Participants))

		for i, p := range matcher.SessionData.Participants {
			publisher := false
			if i == randIndex {
				publisher = true
				p.Publisher = publisher
			}
			p.chanSubmitSignedTxRes <- submitSignedTxRes{
				err:       nil,
				tx:        matcher.SessionData.SignedTx,
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
		matcher.SessionData.PublishedTx = req.tx
		log.Infof("Joined split tx hash %v", req.tx.TxHash())
	}

	if matcher.SessionData.AllSentPublished() {
		for _, p := range matcher.SessionData.Participants {
			p.chanPublishedTxRes <- publishedTxRes{
				tx:  req.tx,
				err: nil,
			}
		}
		log.Info("Sent joined transaction to all participants for purchasing tickets")
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

	//log.Infof("addParticipantInput %v", req.sessionID)

	participant.chanSubmitSplitTxRes = req.resp

	//if this is first participant session then assign splittx to MergedSplitTx
	if matcher.SessionData.MergedSplitTx == nil {
		matcher.SessionData.MergedSplitTx = req.splitTx.Copy()

		for i, _ := range matcher.SessionData.MergedSplitTx.TxIn {
			inputIndes = append(inputIndes, int32(i))
		}
		for i, _ := range matcher.SessionData.MergedSplitTx.TxOut {
			outputIndes = append(outputIndes, int32(i))
		}
	} else {
		k := 0
		inputSize := len(matcher.SessionData.MergedSplitTx.TxIn)
		for _, txin := range req.splitTx.TxIn {
			matcher.SessionData.MergedSplitTx.AddTxIn(txin)
			inputIndes = append(inputIndes, int32(inputSize+k))
			k++
		}
		k = 0
		outputSize := len(matcher.SessionData.MergedSplitTx.TxOut)
		for _, txout := range req.splitTx.TxOut {
			matcher.SessionData.MergedSplitTx.AddTxOut(txout)
			outputIndes = append(outputIndes, int32(outputSize+k))
			k++
		}
	}

	participant.InputIndes = inputIndes
	participant.OutputIndes = outputIndes

	//fmt.Printf("Before. inputindex %v, outputindex %v\r\n", inputIndes, outputIndes)

	if matcher.SessionData.SubTxAdded() {
		matcher.SendTxData()
	}
	return inputIndes, outputIndes, nil
}

func (matcher *Matcher) SendTxData() {
	log.Info("All participants submitted the split transaction, sending merged split transactions back for each participant")
	//needs to random index here
	if matcher.cfg.RandomIndex && len(matcher.sessions) > 1 {

		txInSize := len(matcher.SessionData.MergedSplitTx.TxIn)
		txOutSize := len(matcher.SessionData.MergedSplitTx.TxOut)
		txIn := make([]*wire.TxIn, txInSize)
		txOut := make([]*wire.TxOut, txOutSize)

		//fmt.Println("Enter random index ", txInSize, txOutSize)

		permIn := rand.Perm(txInSize)
		permOut := rand.Perm(txOutSize)

		fn := func(suffe []int, index int) int {
			for i, suffeIndex := range suffe {
				if i == index {
					return suffeIndex
				}
			}
			return -1
		}

		inputIndes := make([]int32, 0)
		outputIndes := make([]int32, 0)

		for _, p := range matcher.SessionData.Participants {

			//fmt.Println("participant ", p.ID)

			for _, i := range p.InputIndes {
				index := fn(permIn, int(i))
				txIn[index] = matcher.SessionData.MergedSplitTx.TxIn[i]
				inputIndes = append(inputIndes, int32(index))
			}

			//fmt.Println("participant - start for output index ", participant.ID)

			for _, i := range p.OutputIndes {
				index := fn(permOut, int(i))
				txOut[index] = matcher.SessionData.MergedSplitTx.TxOut[i]
				outputIndes = append(outputIndes, int32(index))
			}

			p.InputIndes = inputIndes
			p.OutputIndes = outputIndes

			//fmt.Printf("After. inputindex %v, outputindex %v\r\n", inputIndes, outputIndes)
			//fmt.Println("End participant ", p.ID)

			inputIndes = make([]int32, 0)
			outputIndes = make([]int32, 0)
		}

		matcher.SessionData.MergedSplitTx.TxIn = txIn
		matcher.SessionData.MergedSplitTx.TxOut = txOut

		for _, p := range matcher.SessionData.Participants {
			p.chanSubmitSplitTxRes <- submitSplitTxRes{
				err:         nil,
				tx:          matcher.SessionData.MergedSplitTx,
				inputIndes:  p.InputIndes,
				outputIndes: p.OutputIndes,
			}
		}
	} else {

		for _, p := range matcher.SessionData.Participants {
			p.chanSubmitSplitTxRes <- submitSplitTxRes{
				err:         nil,
				tx:          matcher.SessionData.MergedSplitTx,
				inputIndes:  p.InputIndes,
				outputIndes: p.OutputIndes,
			}
		}
	}
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

	matcher.submitSplitTxRequest <- req
	resp := <-req.resp
	return resp.tx, resp.inputIndes, resp.outputIndes, resp.err
}

// SubmitSignedTransaction get signed inputs from parti and
// merge to one transaction
func (matcher *Matcher) SubmitSignedTransaction(sessionID SessionID, signedTx *wire.MsgTx) (*wire.MsgTx, bool, error) {

	req := submitSignedTxRequest{
		sessionID: sessionID,
		tx:        signedTx,
		resp:      make(chan submitSignedTxRes),
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
