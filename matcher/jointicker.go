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

	WaitingQueue struct {
		sync.Mutex
		waitingPars   map[SessionID]*addParticipantReq
		addParReqChan chan addParticipantReq
	}
)

func NewWaitingQueue() *WaitingQueue {
	return &WaitingQueue{
		waitingPars:   make(map[SessionID]*addParticipantReq),
		addParReqChan: make(chan addParticipantReq),
	}
}

func NewTicketJoiner(config *Config) *JoinTicker {
	//log time will start join transaction
	timeStartJoin := time.Now().Add(time.Second * time.Duration(config.JoinTicker))
	log.Info("Will start join session at", util.GetTimeString(timeStartJoin))

	return &JoinTicker{
		joinTicker:   time.NewTicker(time.Second * time.Duration(config.JoinTicker)),
		joinSessions: make(map[SessionID]*JoinSession),
		cfg:          config,
		done:         make(chan bool, 1),
	}
}

func (waitingQueue *WaitingQueue) AddParticipant(maxAmount uint64, sessID SessionID) (*SessionParticipant, error) {

	req := addParticipantReq{
		maxAmount: maxAmount,
		sessID:    sessID,
		resp:      make(chan addParticipantRes),
	}
	waitingQueue.addParReqChan <- req

	resp := <-req.resp
	return resp.participant, resp.err
}

func (joinTicker *JoinTicker) NewSessionID() SessionID {
	id := xid.New()
	return SessionID(id.String())
}

func (joinTicker *JoinTicker) GetJoinSession(sessID string) *JoinSession {
	joinTicker.Lock()
	defer joinTicker.Unlock()
	return joinTicker.joinSessions[SessionID(sessID)]
}
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

func (waitingQueue *WaitingQueue) RemoveWaitingID(sessionID SessionID) error {
	waitingQueue.Lock()
	defer waitingQueue.Unlock()
	_, ok := waitingQueue.waitingPars[sessionID]
	if ok {
		delete(waitingQueue.waitingPars, sessionID)
		log.Infof("SessionID %v disconnected, remove from waiting queue", sessionID)
		return nil
	}
	return ErrSessionIDNotFound
}

func (joinTicker *JoinTicker) Run(waitingQueue *WaitingQueue) {

	for {
		select {

		case req := <-waitingQueue.addParReqChan:
			waitingQueue.Lock()
			waitingQueue.waitingPars[req.sessID] = &req
			waitingQueue.Unlock()

		case <-joinTicker.joinTicker.C:
			timeStartJoin := time.Now().Add(time.Second * time.Duration(joinTicker.cfg.JoinTicker))

			sessSize := len(waitingQueue.waitingPars)
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
			waitingQueue.Lock()
			for _, r := range waitingQueue.waitingPars {
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
			waitingQueue.waitingPars = make(map[SessionID]*addParticipantReq)
			waitingQueue.Unlock()

			//update progress
			joinSession.progress = Waiting_Raw_Tx
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

func (joinTicker *JoinTicker) Stop(completeJoin bool) {
	//if enable complete join option, so server will wait until join completes
	if completeJoin {
		if len(joinTicker.joinSessions) != 0 {
			log.Info("Waiting for join session completes...")
			<-joinTicker.done
		}
	}
}
