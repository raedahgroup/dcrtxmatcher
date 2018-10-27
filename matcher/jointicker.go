package matcher

import (
	"sync"
	"time"

	"github.com/decred/dcrwallet/dcrtxclient/util"
	"github.com/rs/xid"
)

type (
	JoinTicker struct {
		sync.Mutex
		joinTicker   *time.Ticker
		joinSessions map[SessionID]*JoinSession
		cfg          *Config
		done         chan bool
	}

	JoinQueue struct {
		sync.Mutex
		waitingPars   map[SessionID]*addParticipantReq
		addParReqChan chan addParticipantReq
	}
)

// NewWaitingQueue creates new queue for waiting participants.
func NewJoinQueue() *JoinQueue {
	return &JoinQueue{
		waitingPars:   make(map[SessionID]*addParticipantReq),
		addParReqChan: make(chan addParticipantReq),
	}
}

// NewTicketJoiner returns new join ticker engine.
// JoinTicker manages all join sessions.
func NewTicketJoiner(config *Config) *JoinTicker {
	// Log time will start join transaction
	timeStartJoin := time.Now().Add(time.Second * time.Duration(config.JoinTicker))
	log.Info("Will start join session at", util.GetTimeString(timeStartJoin))

	return &JoinTicker{
		joinTicker:   time.NewTicker(time.Second * time.Duration(config.JoinTicker)),
		joinSessions: make(map[SessionID]*JoinSession),
		cfg:          config,
		done:         make(chan bool, 1),
	}
}

// AddParticipant adds participant to join queue
// and responds result back for participant.
func (joinQueue *JoinQueue) AddParticipant(maxAmount uint64, sessID SessionID) (*SessionParticipant, error) {

	req := addParticipantReq{
		maxAmount: maxAmount,
		sessID:    sessID,
		resp:      make(chan addParticipantRes),
	}
	joinQueue.addParReqChan <- req

	resp := <-req.resp
	return resp.participant, resp.err
}

// NewSessionID generates new session id in string
func (joinTicker *JoinTicker) NewSessionID() SessionID {
	id := xid.New()
	return SessionID(id.String())
}

// GetJoinSession returns join session in join ticker with given session id.
func (joinTicker *JoinTicker) GetJoinSession(sessID string) *JoinSession {
	joinTicker.Lock()
	defer joinTicker.Unlock()
	return joinTicker.joinSessions[SessionID(sessID)]
}

// RemoveSession removes join session from join ticker with given session id
func (joinTicker *JoinTicker) RemoveSession(joinId SessionID) {
	joinTicker.Lock()
	defer joinTicker.Unlock()
	delete(joinTicker.joinSessions, joinId)
	log.Debugf("Removed joinId %v after completed", joinId)
	log.Info("Number join sessions in progress", len(joinTicker.joinSessions))

	if len(joinTicker.joinSessions) == 0 {
		joinTicker.done <- true
	}
}

// RemoveWaitingID removes participant from join queue with the given session id.
func (joinQueue *JoinQueue) RemoveWaitingID(sessionID SessionID) error {
	joinQueue.Lock()
	defer joinQueue.Unlock()
	_, ok := joinQueue.waitingPars[sessionID]
	if ok {
		delete(joinQueue.waitingPars, sessionID)
		log.Infof("SessionID %v disconnected, remove from waiting queue", sessionID)
		return nil
	}
	return ErrSessionIDNotFound
}

// Run listens for all incoming participants join request.
// If there are enough participants then create new join session and execute the join session.
func (joinTicker *JoinTicker) Run(joinQueue *JoinQueue) {

	for {
		select {

		case req := <-joinQueue.addParReqChan:
			joinQueue.Lock()
			joinQueue.waitingPars[req.sessID] = &req
			joinQueue.Unlock()

		case <-joinTicker.joinTicker.C:
			timeStartJoin := time.Now().Add(time.Second * time.Duration(joinTicker.cfg.JoinTicker))

			sessSize := len(joinQueue.waitingPars)
			if sessSize == 0 {
				log.Info("Zero participants connected")
				log.Info("Will start next join session at", util.GetTimeString(timeStartJoin))
				continue
			}

			if sessSize < joinTicker.cfg.MinParticipants {
				log.Infof("Number participants %d, will wait for minimum %d", sessSize, joinTicker.cfg.MinParticipants)
				log.Info("Will start next join session at", util.GetTimeString(timeStartJoin))
				continue
			}

			joinSession := NewJoinSession(joinTicker.cfg)

			log.Info("Start join split transaction")
			joinSession.SessionData = &Session{
				Participants: make(map[SessionID]*SessionParticipant, sessSize),
			}
			//add to join session
			joinSessionID := joinSession.NewSessionID()
			joinSession.JoinSessionID = joinSessionID
			i := 0
			joinQueue.Lock()
			for _, r := range joinQueue.waitingPars {
				sessPart := &SessionParticipant{
					Index:         i,
					ID:            r.sessID,
					JoinSessionID: joinSessionID,
					Session:       joinSession.SessionData,
				}

				i++
				joinSession.SessionData.Participants[r.sessID] = sessPart
				r.resp <- addParticipantRes{
					participant: sessPart,
				}
			}
			joinQueue.waitingPars = make(map[SessionID]*addParticipantReq)
			joinQueue.Unlock()

			//update progress
			joinSession.progress = StateRawTx
			//add timer for waiting participants send inputs
			joinSession.clientTimeout = time.NewTimer(time.Second * time.Duration(joinTicker.cfg.WaitingTimer))
			joinTicker.Lock()
			joinTicker.joinSessions[joinSessionID] = joinSession
			joinSession.joinTicker = joinTicker
			joinTicker.Unlock()

			log.Infof("JoinSessionId %v", joinSessionID)

			go joinSession.Run()
		}
	}
}

// Stop stops join sticker engine.
// If there is still one join session running,
// it will waits for join session finishes before terminate.
func (joinTicker *JoinTicker) Stop(completeJoin bool) {
	//if enable complete join option, so server will wait until join completes
	if completeJoin {
		if len(joinTicker.joinSessions) != 0 {
			log.Info("Waiting for join session completes...")
			<-joinTicker.done
		}
	}
}
