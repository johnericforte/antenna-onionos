// Package dbg is the app's trace log, switched on by an environment variable.
//
// A handheld has no console and no debugger, so the only way to see what the
// app did is a file on the card. That trace is worth having during
// development and is noise in a release, which is why it is off unless asked
// for. Failures the user needs to act on go on the screen either way.
package dbg

import (
	"log"
	"os"
	"strconv"
)

// EnvVar switches tracing on when it is set to a true value: 1, t, T, TRUE,
// true, True. launch.sh sets it to 0 for a release build.
const EnvVar = "ANTENNA_DEBUG"

var enabled bool

// Init reads the environment and prepares the log. It reports whether tracing
// is on, so the caller can say so in the first line of the file.
func Init() bool {
	enabled, _ = strconv.ParseBool(os.Getenv(EnvVar))
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	return enabled
}

// On reports whether tracing is enabled.
func On() bool { return enabled }

// Printf records one step. It costs a function call and an env lookup that
// already happened when tracing is off.
func Printf(format string, args ...any) {
	if !enabled {
		return
	}
	log.Printf(format, args...)
}

// Fail records something that went wrong. Failures are always written, even in
// a release, because a user reporting a problem has nothing else to send.
func Fail(format string, args ...any) {
	log.Printf(format, args...)
}
