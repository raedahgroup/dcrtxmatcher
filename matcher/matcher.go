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

const (
	Waiting_Participant = iota + 1
	Waiting_Raw_Tx
	Waiting_Signed_Tx
	Waiting_Publish_Tx
	Completed
)

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
		//Participants []*SessionParticipant
		Participants map[SessionID]*SessionParticipant

		MergedSplitTx *wire.MsgTx
		SignedTx      *wire.MsgTx
		PublishedTx   *wire.MsgTx
		PublishIndex  int
	}
	// Config stores the parameters for the matcher engine
	Config struct {
		MinParticipants int
		RandomIndex     bool
		JoinTicker      int
		WaitingTimer    int
	}

	// Matcher is the main engine for matching operations
	Matcher struct {
		cfg                 *Config
		waitingParticipants map[SessionID]*addParticipantReq
		SessionData         *Session

		addParticipantReq chan addParticipantReq
		submitSplitTxReq  chan submitSplitTxReq
		submitSignedTxReq chan submitSignedTxReq
		publishedTxReq    chan publishedTxReq

		sessionTicker *time.Ticker
		clientTimeout *time.Timer
		progress      int
	}
)

// InputsSigned returns true if all participant already sent their signed input/output
func (sess *Session) CheckInputsSigned() bool {
	for _, p := range sess.Participants {
		if p.SignedTx == nil {
			return false
		}
	}
	return true
}

//AllSentPublished returns true if all participant already sent their publish result
func (sess *Session) CheckAllSentPublished() bool {
	for _, p := range sess.Participants {
		if !p.SentPublished {
			return false
		}
	}
	return true
}

// SubTxAdded returns true if all participant already sent their invidual input/output tx
func (sess *Session) CheckTxSubmitted() bool {
	for _, p := range sess.Participants {
		if p.SplitTx == nil {
			return false
		}
	}
	return true
}

func NewMatcher(cfg *Config) *Matcher {
	m := &Matcher{
		cfg:                 cfg,
		waitingParticipants: make(map[SessionID]*addParticipantReq),
		addParticipantReq:   make(chan addParticipantReq),
		submitSplitTxReq:    make(chan submitSplitTxReq),
		submitSignedTxReq:   make(chan submitSignedTxReq),
		publishedTxReq:      make(chan publishedTxReq),
		sessionTicker:       time.NewTicker(time.Second * time.Duration(cfg.JoinTicker)),
		clientTimeout:       time.NewTimer(time.Second * time.Duration(cfg.JoinTicker)),
		progress:            Waiting_Participant,
	}

	return m
}

// Run listens for all matcher messages and runs the matching engine.
func (matcher *Matcher) Run() error {
	for {

		select {
		case req := <-matcher.addParticipantReq:
			// validate the grpc request with current matching progress
			if matcher.progress != Waiting_Participant {
				req.resp <- addParticipantRes{
					err: ErrInvalidRequest,
				}
			} else {
				matcher.waitingParticipants[req.sessID] = &req
			}
		case <-matcher.sessionTicker.C:
			if matcher.progress == Waiting_Participant {
				matcher.startJoinSession()
			}
		case <-matcher.clientTimeout.C:
			//check participants who missing inputs and remove
			missedP := make([]SessionID, 0)
			if matcher.SessionData != nil {
				switch matcher.progress {
				case Waiting_Raw_Tx:
					for sid, p := range matcher.SessionData.Participants {
						if p.SplitTx == nil {
							missedP = append(missedP, sid)
							log.Infof("Session progress Waiting_Raw_Tx. sessionID %v not sending raw tx data", sid)
						}
					}
					break

				case Waiting_Signed_Tx:
					for sid, p := range matcher.SessionData.Participants {
						if p.SignedTx == nil {
							missedP = append(missedP, sid)
							log.Infof("Session progress Waiting_Signed_Tx. sessionID %v not sending signed tx data", sid)
						}
					}
					break
				case Waiting_Publish_Tx:
					break
				}
			}

			if len(missedP) > 0 {
				for _, sid := range missedP {
					log.Infof("Timeout. Remove sessionID %v from Session", sid)
					delete(matcher.SessionData.Participants, sid)
				}
			}

		case req := <-matcher.submitSplitTxReq:

			// validate the grpc request with current matching progress
			if matcher.progress != Waiting_Raw_Tx {
				req.resp <- submitSplitTxRes{
					err: ErrInvalidRequest,
				}
			} else {
				_, _, err := matcher.addParticipantInput(&req)
				if err != nil {
					req.resp <- submitSplitTxRes{
						err: err,
					}
				}
			}
		case req := <-matcher.submitSignedTxReq:
			// validate the grpc request with current matching progress
			if matcher.progress != Waiting_Signed_Tx {
				req.resp <- submitSignedTxRes{
					err: ErrInvalidRequest,
				}
			} else {
				err := matcher.mergeSignedInput(&req)
				if err != nil {
					req.resp <- submitSignedTxRes{
						err: err,
					}
				}
			}
		case req := <-matcher.publishedTxReq:
			// validate the grpc request with current matching progress
			if matcher.progress != Waiting_Publish_Tx {
				req.resp <- publishedTxRes{
					err: ErrInvalidRequest,
				}
			} else {
				err := matcher.publishTxResult(&req)
				if err != nil {
					req.resp <- publishedTxRes{
						err: err,
					}
				}
			}
		}
	}
}

//check wether enough users to start merge splittx
func (matcher *Matcher) enoughForMergeTx() bool {
	return len(matcher.waitingParticipants) == matcher.cfg.MinParticipants
}

func (matcher *Matcher) startJoinSession() {
	sessSize := len(matcher.waitingParticipants)
	if sessSize == 0 {
		return
	}
	// there is only one participant, then returns error for client to purchase locally
	if sessSize == 1 {
		log.Info("Only one participant, returns error for client purchasing locally")
		for _, r := range matcher.waitingParticipants {
			sessPart := &SessionParticipant{
				Session: matcher.SessionData,
				Index:   0,
				ID:      r.sessID,
			}
			r.resp <- addParticipantRes{
				participant: sessPart,
				err:         errors.New("One participant only"),
			}
		}
		matcher.waitingParticipants = make(map[SessionID]*addParticipantReq)
		return
	}

	if sessSize < matcher.cfg.MinParticipants {
		log.Infof("Number participants %d, not enough minimum required %d, waiting a moment", sessSize, matcher.cfg.MinParticipants)
		return
	}

	log.Info("Start join split transaction")
	matcher.SessionData = &Session{
		Participants: make(map[SessionID]*SessionParticipant, sessSize),
	}

	i := 0
	for _, r := range matcher.waitingParticipants {
		sessPart := &SessionParticipant{
			Session: matcher.SessionData,
			Index:   i,
			ID:      r.sessID,
		}

		i++
		matcher.SessionData.Participants[r.sessID] = sessPart
		r.resp <- addParticipantRes{
			participant: sessPart,
		}
	}
	//update progress
	matcher.progress = Waiting_Raw_Tx
	//add timer for waiting participants send inputs
	matcher.clientTimeout = time.NewTimer(time.Second * time.Duration(matcher.cfg.WaitingTimer))

	matcher.waitingParticipants = make(map[SessionID]*addParticipantReq)
}

func (matcher *Matcher) NewSessionID() SessionID {
	id := xid.New()
	return SessionID(id.String())
}

func (matcher *Matcher) mergeSignedInput(req *submitSignedTxReq) error {

	if _, has := matcher.SessionData.Participants[req.sessionID]; !has {
		return ErrSessionNotFound
	}
	participant := matcher.SessionData.Participants[req.sessionID]
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

	if matcher.SessionData.CheckInputsSigned() {
		//select random participant to publish transaction
		randIndex := rand.Intn(len(matcher.SessionData.Participants))
		i := 0
		for _, p := range matcher.SessionData.Participants {
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
			i++
		}
		matcher.progress = Waiting_Publish_Tx
	}
	return nil
}

func (matcher *Matcher) publishTxResult(req *publishedTxReq) error {
	if _, has := matcher.SessionData.Participants[req.sessionID]; !has {
		return ErrSessionNotFound
	}
	participant := matcher.SessionData.Participants[req.sessionID]

	participant.SentPublished = true
	participant.chanPublishedTxRes = req.resp

	if req.tx != nil && participant.Publisher {
		matcher.SessionData.PublishedTx = req.tx
		log.Infof("Joined split transaction hash %v", req.tx.TxHash())
	}

	if matcher.SessionData.CheckAllSentPublished() {
		for _, p := range matcher.SessionData.Participants {
			if matcher.SessionData.PublishedTx != nil {
				p.chanPublishedTxRes <- publishedTxRes{
					tx:  matcher.SessionData.PublishedTx,
					err: nil,
				}
			} else {
				p.chanPublishedTxRes <- publishedTxRes{
					tx:  nil,
					err: ErrPublishTxNotSent,
				}
			}
		}
		log.Info("Sent joined transaction to all participants for purchasing tickets")
		//update progress to waiting participant for next session
		matcher.progress = Waiting_Participant
	}

	return nil

}

func (matcher *Matcher) RemoveSessionID(sessionID SessionID) error {
	_, ok := matcher.SessionData.Participants[sessionID]
	if ok {
		delete(matcher.SessionData.Participants, sessionID)
		log.Infof("Participant left, removed sessionID %v", sessionID)
		return nil
	}
	return ErrSessionIDNotFound
}

func (matcher *Matcher) RemoveWaitingSessionID(sessionID SessionID) error {
	_, ok := matcher.waitingParticipants[sessionID]
	if ok {
		delete(matcher.waitingParticipants, sessionID)
		log.Infof("Participant disconnected. Removed sessionID %v from waiting queue", sessionID)
		return nil
	}
	return ErrSessionIDNotFound
}

func (matcher *Matcher) addParticipantInput(req *submitSplitTxReq) ([]int32, []int32, error) {

	if _, has := matcher.SessionData.Participants[req.sessionID]; !has {
		return nil, nil, ErrSessionNotFound
	}

	inputIndes := make([]int32, 0)
	outputIndes := make([]int32, 0)

	participant := matcher.SessionData.Participants[req.sessionID]
	participant.SplitTx = req.splitTx

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

	if matcher.SessionData.CheckTxSubmitted() {
		matcher.SendTxData()
	}
	return inputIndes, outputIndes, nil
}

func (matcher *Matcher) SendTxData() {
	log.Info("All participants submitted the split transaction, sending the joined split transactions back to each participant")
	//needs to random index here
	if matcher.cfg.RandomIndex && len(matcher.SessionData.Participants) > 1 {

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
		//update progress
		matcher.progress = Waiting_Signed_Tx
		//add timer for waiting participants send signed split tx
		matcher.clientTimeout = time.NewTimer(time.Second * time.Duration(matcher.cfg.WaitingTimer))
	} else {

		for _, p := range matcher.SessionData.Participants {
			p.chanSubmitSplitTxRes <- submitSplitTxRes{
				err:         nil,
				tx:          matcher.SessionData.MergedSplitTx,
				inputIndes:  p.InputIndes,
				outputIndes: p.OutputIndes,
			}
		}
		//add timer for waiting participants send signed split tx
		matcher.clientTimeout = time.NewTimer(time.Second * time.Duration(matcher.cfg.WaitingTimer))
	}
}

func (matcher *Matcher) AddParticipant(maxAmount uint64, sessID SessionID) (*SessionParticipant, error) {

	req := addParticipantReq{
		maxAmount: maxAmount,
		sessID:    sessID,
		resp:      make(chan addParticipantRes),
	}
	matcher.addParticipantReq <- req

	resp := <-req.resp
	return resp.participant, resp.err
}

// PublishTransaction validates the signed input provided by one of the
// participants of the given session and publishes the transaction. It blocks
// until all participants sent their inputs
func (matcher *Matcher) SubmitSplitTx(sessionID SessionID, splitTx *wire.MsgTx,
	splitTxOutputIndex int, input *wire.TxIn) (*wire.MsgTx, []int32, []int32, error) {

	req := submitSplitTxReq{
		sessionID:          sessionID,
		splitTx:            splitTx,
		input:              input,
		splitTxOutputIndex: splitTxOutputIndex,
		resp:               make(chan submitSplitTxRes),
	}

	matcher.submitSplitTxReq <- req
	resp := <-req.resp
	return resp.tx, resp.inputIndes, resp.outputIndes, resp.err
}

// SubmitSignedTransaction get signed inputs from parti and
// merge to one transaction
func (matcher *Matcher) SubmitSignedTransaction(sessionID SessionID, signedTx *wire.MsgTx) (*wire.MsgTx, bool, error) {

	req := submitSignedTxReq{
		sessionID: sessionID,
		tx:        signedTx,
		resp:      make(chan submitSignedTxRes),
	}

	matcher.submitSignedTxReq <- req
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
