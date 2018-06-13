package daemon

// Config stores the config needed to run an instance of the dcr split ticket
// matcher daemon
type Config struct {
	Port            int
	MinParticipants int
	RandomIndex     bool
	JoinTicker      int
	WaitingTimer    int
}
