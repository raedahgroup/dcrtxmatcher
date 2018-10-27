package matcher

import (
	"github.com/ansel1/merry"
)

var (
	ErrLowAmount           = merry.New("Amount too low to participate in ticket purchase")
	ErrTooManyParticipants = merry.New("Too many online participants at the moment")
	ErrSessionNotFound     = merry.New("Session with the provided ID not found")
	ErrSessionIDNotFound   = merry.New("SessionID is not found")
	ErrNilCommitmentOutput = merry.New("Nil commitment output provided")
	ErrNilChangeOutput     = merry.New("Nil change output provided")
	ErrInvalidRequest      = merry.New("Invalid request")
	ErrIndexNotFound       = merry.New("Index not found")
	ErrPublishTxNotSent    = merry.New("Published transaction is not sent to server")
	ErrParticipantLeft     = merry.New("One participant has left session")
)
