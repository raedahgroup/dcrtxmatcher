package matcher

import (
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/decred/dcrd/wire"
	"github.com/rs/xid"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

const (
	StateParticipant = iota + 1
	StateRawTx
	StateSignedTx
	StatePublishTx
	StateCompleted
)

type (
	// SessionID stores the unique id for an in-progress join split transaction.
	SessionID string

	// SessionParticipant is participant data in join session.
	SessionParticipant struct {
		ID            SessionID
		JoinSessionID SessionID
		SplitTx       *wire.MsgTx
		SignedTx      *wire.MsgTx
		Session       *Session
		Index         int

		InputIndes    []int32
		OutputIndes   []int32
		Publisher     bool
		SentPublished bool

		chanSubmitSplitTxRes  chan submitSplitTxRes
		chanSubmitSignedTxRes chan submitSignedTxRes
		chanPublishedTxRes    chan publishedTxRes
	}

	// Session is a join transaction.
	Session struct {
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

	// Matcher is the main engine for matching operation
	JoinSession struct {
		JoinSessionID SessionID
		cfg           *Config
		SessionData   *Session

		addParticipantReq chan addParticipantReq
		submitSplitTxReq  chan submitSplitTxReq
		submitSignedTxReq chan submitSignedTxReq
		publishedTxReq    chan publishedTxReq

		sessionTicker *time.Ticker
		clientTimeout *time.Timer
		progress      int
		done          chan bool

		joinTicker *JoinTicker
	}
)

// CheckInputsSigned returns true if all participants already sent their signed transaction input, output.
func (sess *Session) CheckInputsSigned() bool {
	for _, p := range sess.Participants {
		if p.SignedTx == nil {
			return false
		}
	}
	return true
}

// CheckAllSentPublished returns true if all participants already sent their publish result.
func (sess *Session) CheckAllSentPublished() bool {
	for _, p := range sess.Participants {
		if !p.SentPublished {
			return false
		}
	}
	return true
}

// CheckTxSubmitted returns true if all participants already sent their transaction input, output.
func (sess *Session) CheckTxSubmitted() bool {
	for _, p := range sess.Participants {
		if p.SplitTx == nil {
			return false
		}
	}
	return true
}

// NewJoinSession creates new join session.
func NewJoinSession(cfg *Config) *JoinSession {
	m := &JoinSession{
		cfg:               cfg,
		addParticipantReq: make(chan addParticipantReq),
		submitSplitTxReq:  make(chan submitSplitTxReq),
		submitSignedTxReq: make(chan submitSignedTxReq),
		publishedTxReq:    make(chan publishedTxReq),
		clientTimeout:     time.NewTimer(time.Second * time.Duration(cfg.JoinTicker)),
		progress:          StateParticipant,
		done:              make(chan bool, 1),
	}
	// Log time will start join transaction
	timeStartJoin := time.Now().Add(time.Second * time.Duration(cfg.JoinTicker))
	log.Info("Will start join session at ", timeStartJoin.Format("2006-01-02 15:04:05"))

	return m
}

// Run listens for all request messages from clients and runs the matching engine.
func (matcher *JoinSession) Run() error {

	for {

		select {
		// We use one timer to control participants sending data in time.
		// With one progress, server has max time for client process is 30 seconds (setting in config file)
		// After that time, client still not send data, server will consider client is malicious and remove
		case <-matcher.clientTimeout.C:
			// Check participants who not sending data and remove.
			missedP := make([]SessionID, 0)
			if matcher.SessionData != nil {
				switch matcher.progress {
				case StateRawTx:
					for sid, p := range matcher.SessionData.Participants {
						if p.SplitTx == nil {
							missedP = append(missedP, sid)
							log.Infof("Session progress StateRawTx. sessionID %v not sending raw tx data", sid)
						}
					}
					break
				case StateSignedTx:
					for sid, p := range matcher.SessionData.Participants {
						if p.SignedTx == nil {
							missedP = append(missedP, sid)
							log.Infof("Session progress StateSignedTx. sessionID %v not sending signed tx data", sid)
						}
					}
					break
				case StatePublishTx:
					for sid, p := range matcher.SessionData.Participants {
						if p.SignedTx == nil {
							missedP = append(missedP, sid)
							log.Infof("Session progress StatePublishTx. sessionID %v not sending signed tx data", sid)
						}
					}
					break
				}
			}

			if len(missedP) > 0 {
				for _, sid := range missedP {
					log.Infof("Timeout. Client with sessionID %v stopped responding", sid)
					delete(matcher.SessionData.Participants, sid)
				}
			}

		case req := <-matcher.submitSplitTxReq:
			// In every round, server needs to check whether client has sent
			// data that is inconsistent with join session progress.
			if matcher.progress != StateRawTx {
				log.Errorf("Client sent invalid progress command %v. Current progress is %d", "StateRawTx", matcher.progress)
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
			if matcher.progress != StateSignedTx {
				log.Errorf("Client sent invalid progress command %v. Current progress is %d", "StateSignedTx", matcher.progress)
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
			if matcher.progress != StatePublishTx {
				log.Errorf("Client sent invalid progress command %v. Current progress is %d", "StatePublishTx", matcher.progress)
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

// NewSessionID generates new string session id.
func (matcher *JoinSession) NewSessionID() SessionID {
	id := xid.New()
	return SessionID(id.String())
}

// mergeSignedInput merges signed transaction data of each participant to one.
func (matcher *JoinSession) mergeSignedInput(req *submitSignedTxReq) error {

	if _, has := matcher.SessionData.Participants[req.sessionID]; !has {
		return ErrSessionNotFound
	}
	participant := matcher.SessionData.Participants[req.sessionID]
	participant.SignedTx = req.tx

	participant.chanSubmitSignedTxRes = req.resp

	// With first sender, set sign transaction to transaction sent by participant
	if matcher.SessionData.SignedTx == nil {
		matcher.SessionData.SignedTx = req.tx.Copy()
	}
	msg := fmt.Sprintf("SessionID %v submit invalid(nil) input", participant.ID)

	for _, i := range participant.InputIndes {
		if participant.SignedTx.TxIn[i] == nil {
			return errors.New(msg)
		}
		matcher.SessionData.SignedTx.TxIn[i] = participant.SignedTx.TxIn[i]
	}
	for _, i := range participant.OutputIndes {
		if participant.SignedTx.TxOut[i] == nil {
			return errors.New(msg)
		}
		matcher.SessionData.SignedTx.TxOut[i] = participant.SignedTx.TxOut[i]
	}

	if matcher.SessionData.CheckInputsSigned() {
		// Select random participant to publish transaction
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
		matcher.progress = StatePublishTx
	}
	return nil
}

// publishTxResult publishes the joined transaction to network.
func (matcher *JoinSession) publishTxResult(req *publishedTxReq) error {
	if _, has := matcher.SessionData.Participants[req.sessionID]; !has {
		return ErrSessionNotFound
	}
	participant := matcher.SessionData.Participants[req.sessionID]

	participant.SentPublished = true
	participant.chanPublishedTxRes = req.resp

	if req.tx != nil && participant.Publisher {
		matcher.SessionData.PublishedTx = req.tx
		log.Infof("Joined split transaction hash %v", req.tx.TxHash())
	} else if req.tx == nil && participant.Publisher {
		// Publish transaction failed from client
		matcher.SessionData.PublishedTx = req.tx
		log.Infof("Publish transaction failed on client side")
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
		if matcher.SessionData.PublishedTx != nil {
			log.Infof("Sent joined transaction to %v participants for purchasing tickets", len(matcher.SessionData.Participants))
		}

		matcher.joinTicker.RemoveSession(matcher.JoinSessionID)

	}
	return nil
}

// RemoveSessionID removes participant from join session with provided session id.
func (matcher *JoinSession) RemoveSessionID(sessionID SessionID) error {
	_, ok := matcher.SessionData.Participants[sessionID]
	if ok {
		delete(matcher.SessionData.Participants, sessionID)
		log.Infof("SessionID %v disconnected, remove from join session", sessionID)
		return nil
	}
	return ErrSessionIDNotFound
}

// addParticipantInput adds transaction data of each participant to joined transaction.
func (matcher *JoinSession) addParticipantInput(req *submitSplitTxReq) ([]int32, []int32, error) {

	if _, has := matcher.SessionData.Participants[req.sessionID]; !has {
		return nil, nil, ErrSessionNotFound
	}

	inputIndes := make([]int32, 0)
	outputIndes := make([]int32, 0)

	participant := matcher.SessionData.Participants[req.sessionID]
	participant.SplitTx = req.splitTx

	participant.chanSubmitSplitTxRes = req.resp

	// Clone the sending transaction if this is first sending participant.
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

	if matcher.SessionData.CheckTxSubmitted() {
		matcher.SendTxData()
	}
	return inputIndes, outputIndes, nil
}

// SendTxData sends merged transaction data to each participant.
// This includes merged transaction from all participants and participant's input and output index.
func (matcher *JoinSession) SendTxData() {
	log.Debug("Send the joined split transactions back to each participant")
	// If RandomIndex option is enable, server swaps randomly both input and output index.
	if matcher.cfg.RandomIndex && len(matcher.SessionData.Participants) > 1 {

		txInSize := len(matcher.SessionData.MergedSplitTx.TxIn)
		txOutSize := len(matcher.SessionData.MergedSplitTx.TxOut)
		txIn := make([]*wire.TxIn, txInSize)
		txOut := make([]*wire.TxOut, txOutSize)

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

			for _, i := range p.InputIndes {
				index := fn(permIn, int(i))
				txIn[index] = matcher.SessionData.MergedSplitTx.TxIn[i]
				inputIndes = append(inputIndes, int32(index))
			}

			for _, i := range p.OutputIndes {
				index := fn(permOut, int(i))
				txOut[index] = matcher.SessionData.MergedSplitTx.TxOut[i]
				outputIndes = append(outputIndes, int32(index))
			}

			p.InputIndes = inputIndes
			p.OutputIndes = outputIndes

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
		// Update join session progress.
		matcher.progress = StateSignedTx
		// Add timer for next join step.
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
		// Add timer for next join step.
		matcher.clientTimeout = time.NewTimer(time.Second * time.Duration(matcher.cfg.WaitingTimer))
	}
}

// AddParticipant adds join request join transaction and
// returns join session data when enough participants for join session.
func (matcher *JoinSession) AddParticipant(maxAmount uint64, sessID SessionID) (*SessionParticipant, error) {

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
// until all participants sent their inputs.
func (matcher *JoinSession) SubmitSplitTx(sessionID SessionID, splitTx *wire.MsgTx,
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

// SubmitSignedTransaction gets signed inputs from participants and
// build to one transaction.
func (matcher *JoinSession) SubmitSignedTx(sessionID SessionID, signedTx *wire.MsgTx) (*wire.MsgTx, bool, error) {

	req := submitSignedTxReq{
		sessionID: sessionID,
		tx:        signedTx,
		resp:      make(chan submitSignedTxRes),
	}

	matcher.submitSignedTxReq <- req
	resp := <-req.resp
	return resp.tx, resp.publisher, resp.err
}

// PublishResult receives published transaction from one participant,
// sending to other participants.
func (matcher *JoinSession) PublishResult(sessionID SessionID, signedTx *wire.MsgTx) (*wire.MsgTx, error) {

	req := publishedTxReq{
		sessionID: sessionID,
		tx:        signedTx,
		resp:      make(chan publishedTxRes),
	}

	matcher.publishedTxReq <- req
	resp := <-req.resp
	return resp.tx, resp.err
}
