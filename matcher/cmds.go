package matcher

import (
	"github.com/decred/dcrd/dcrutil"
	"github.com/decred/dcrd/wire"
)

type (
	addParticipantResponse struct {
		participant *SessionParticipant
		err         error
	}

	setParticipantOutputsResponse struct {
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
	addParticipantRequest struct {
		maxAmount uint64
		sessID    SessionID
		resp      chan addParticipantResponse
	}

	setParticipantOutputsRequest struct {
		sessionID        SessionID
		commitmentOutput *wire.TxOut
		changeOutput     *wire.TxOut
		voteAddress      *dcrutil.Address
		resp             chan setParticipantOutputsResponse
	}

	submitSplitTxReq struct {
		sessionID          SessionID
		splitTx            *wire.MsgTx
		input              *wire.TxIn
		splitTxOutputIndex int
		resp               chan submitSplitTxRes
	}

	submitSignedTxRequest struct {
		sessionID SessionID
		tx        *wire.MsgTx
		resp      chan submitSignedTxResponse
	}

	submitSignedTxResponse struct {
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

	publishSessionRequest struct {
		session *Session
	}
)
