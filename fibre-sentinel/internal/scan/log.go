package scan

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Logger writes prefixed lines to stdout and keeps the last ringSize of them in
// memory. On a fatal error (including any wait that times out) Dump prints the
// ring to stderr so a stuck scanner always leaves a trail, instead of hanging.
type Logger struct {
	mu   sync.Mutex
	ring []string
	max  int
}

// NewLogger returns a Logger that keeps the last max lines.
func NewLogger(max int) *Logger {
	if max <= 0 {
		max = 200
	}
	return &Logger{max: max}
}

// Printf logs one line.
func (l *Logger) Printf(format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	stamp := time.Now().UTC().Format("15:04:05.000")
	full := "SENT| " + stamp + " " + line
	l.mu.Lock()
	l.ring = append(l.ring, full)
	if len(l.ring) > l.max {
		l.ring = l.ring[len(l.ring)-l.max:]
	}
	l.mu.Unlock()
	fmt.Println(full)
}

// Dump writes the retained ring to stderr. Used right before a fatal exit.
func (l *Logger) Dump() {
	l.mu.Lock()
	lines := append([]string(nil), l.ring...)
	l.mu.Unlock()
	fmt.Fprintf(os.Stderr, "\nSENT-DUMP| last %d log lines before failure:\n", len(lines))
	for _, ln := range lines {
		fmt.Fprintln(os.Stderr, "  "+ln)
	}
}

// Fatalf dumps the ring and exits non-zero. Every timeout path ends here.
func (l *Logger) Fatalf(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	l.Printf("FATAL: %s", msg)
	l.Dump()
	fmt.Fprintf(os.Stderr, "SENT-FATAL: %s\n", msg)
	os.Exit(1)
}
