package daemon

// Config stores the config needed to run an instance of the dcr split ticket
// matcher daemon
type Config struct {
	Port            int
	MaxParticipants int
}

// DefaultConfig stores the default, built-in config for the daemon
//var DefaultConfig = &Config{
//	Port:     8475,
//	LogLevel: logging.INFO,
//}
