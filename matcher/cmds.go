package matcher

import (
	"github.com/decred/dcrd/dcrutil"
	"github.com/decred/dcrd/wire"
)

type (
	addParticipantRes struct {
		participant *SessionParticipant
		err         error
	}

	setParticipantOutputsRes struct {
		transaction  *wire.MsgTx
		output_index int
		err          error
	}

	submitSplitTxRes struct {
		tx          *wire.MsgTx
		inputIndes  []int32
		outputIndes []int32
		err         error
	}
)

type (
	addParticipantReq struct {
		maxAmount uint64
		sessID    SessionID
		resp      chan addParticipantRes
	}

	setParticipantOutputsReq struct {
		sessionID        SessionID
		commitmentOutput *wire.TxOut
		changeOutput     *wire.TxOut
		voteAddress      *dcrutil.Address
		resp             chan setParticipantOutputsRes
	}

	submitSplitTxReq struct {
		sessionID          SessionID
		splitTx            *wire.MsgTx
		input              *wire.TxIn
		splitTxOutputIndex int
		resp               chan submitSplitTxRes
	}

	submitSignedTxReq struct {
		sessionID SessionID
		tx        *wire.MsgTx
		resp      chan submitSignedTxRes
	}

	submitSignedTxRes struct {
		tx        *wire.MsgTx
		publisher bool
		err       error
	}
	publishedTxReq struct {
		sessionID SessionID
		tx        *wire.MsgTx
		resp      chan publishedTxRes
	}
	publishedTxRes struct {
		tx  *wire.MsgTx
		err error
	}

	publishSessionReq struct {
		session *Session
	}
)
