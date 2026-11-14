package trace

import (
	"bytes"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// global is the process tracer: nil until Init enables tracing and again
// after Close. Readers load it once; a Tracer that Close detached may still be
// held by an in-flight Emit, which then finds it closed and drops the event.
var global atomic.Pointer[Tracer]

// Tracer writes trace events to a file. It is on while file is set; Close,
// or a failed write, turns it off for good.
type Tracer struct {
	mu            sync.Mutex
	file          *os.File // nil once off
	subsystems    map[string]bool
	level         Level
	startTime     time.Time
	lastTime      sync.Map // subsystem -> time.Time
	eventsWritten atomic.Int64
}

// Init initializes the global tracer from environment variables.
// HASH_TRACE: comma-separated subsystems (e.g., "editor,agent,all")
// HASH_TRACE_PATH: output file path (default: hash-trace.jsonl)
// HASH_TRACE_LEVEL: verbose, detailed, high (default: verbose)
func Init() error {
	traceEnv := strings.TrimSpace(os.Getenv("HASH_TRACE"))
	if traceEnv == "" {
		return nil
	}

	// Parse subsystems
	subsystems := make(map[string]bool)
	for _, s := range strings.Split(traceEnv, ",") {
		s = strings.TrimSpace(strings.ToLower(s))
		if s != "" {
			subsystems[s] = true
		}
	}
	if len(subsystems) == 0 {
		return nil
	}

	// Parse level
	level := LevelVerbose
	if lvl := strings.TrimSpace(strings.ToLower(os.Getenv("HASH_TRACE_LEVEL"))); lvl != "" {
		switch lvl {
		case "detailed":
			level = LevelDetailed
		case "high":
			level = LevelHigh
		}
	}

	// Open file
	path := strings.TrimSpace(os.Getenv("HASH_TRACE_PATH"))
	if path == "" {
		path = "hash-trace.jsonl"
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644) //nolint:gosec // G302: trace logs are non-sensitive
	if err != nil {
		return err
	}

	t := &Tracer{
		file:       f,
		subsystems: subsystems,
		level:      level,
		startTime:  time.Now(),
	}
	if old := global.Swap(t); old != nil {
		old.close()
	}

	// Write start entry
	subList := make([]string, 0, len(subsystems))
	for s := range subsystems {
		subList = append(subList, s)
	}
	t.writeEntry(StartEntry("0.1.0", subList, level, os.Getpid()))

	return nil
}

// Close closes the global tracer.
func Close() {
	if t := global.Swap(nil); t != nil {
		t.close()
	}
}

// close writes the end entry and closes the file; later writes are dropped.
func (t *Tracer) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.file == nil {
		return
	}
	t.writeEntryLocked(EndEntry(time.Since(t.startTime).Milliseconds(), t.eventsWritten.Load()))
	_ = t.file.Close() // best effort
	t.file = nil
}

// Enabled returns true if the subsystem is being traced.
func Enabled(subsystem string) bool {
	return global.Load().enabled(subsystem)
}

func (t *Tracer) enabled(subsystem string) bool {
	if t == nil {
		return false
	}
	return t.subsystems["all"] || t.subsystems[subsystem]
}

// Emit writes a trace event.
func Emit(subsystem, event string, level Level, data any) {
	t := global.Load()
	if t == nil || !t.enabled(subsystem) || !levelEnabled(t.level, level) {
		return
	}

	now := time.Now()

	// Calculate delta from last event in this subsystem
	var deltaMs float64
	if last, ok := t.lastTime.Load(subsystem); ok {
		deltaMs = float64(now.Sub(last.(time.Time)).Microseconds()) / 1000.0
	}
	t.lastTime.Store(subsystem, now)

	entry := Entry{
		Timestamp: now.UTC().Format(time.RFC3339Nano),
		DeltaMs:   deltaMs,
		Goroutine: getGoroutineID(),
		Subsystem: subsystem,
		Level:     level,
		Event:     event,
		Data:      data,
	}

	t.writeEntry(entry)
}

func (t *Tracer) writeEntry(e Entry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.writeEntryLocked(e)
}

// writeEntryLocked writes e unless the tracer is off. Must hold mu.
func (t *Tracer) writeEntryLocked(e Entry) {
	if t.file == nil {
		return // off
	}

	data, err := json.Marshal(e)
	if err != nil {
		return
	}

	var buf bytes.Buffer
	buf.Write(data)
	buf.WriteByte('\n')

	if _, err := t.file.Write(buf.Bytes()); err != nil {
		// Say so once and turn off
		os.Stderr.WriteString("hash: trace write failed, disabling: " + err.Error() + "\n")
		_ = t.file.Close()
		t.file = nil
		return
	}

	t.eventsWritten.Add(1)
}

func levelEnabled(configured, event Level) bool {
	// verbose >= detailed >= high
	switch configured {
	case LevelVerbose:
		return true
	case LevelDetailed:
		return event != LevelVerbose
	case LevelHigh:
		return event == LevelHigh
	}
	return true
}

// getGoroutineID extracts goroutine ID from stack.
func getGoroutineID() int {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// Format: "goroutine 123 [running]:\n..."
	s := string(buf[:n])
	if !strings.HasPrefix(s, "goroutine ") {
		return 0
	}
	s = s[10:]
	idx := strings.IndexByte(s, ' ')
	if idx == -1 {
		return 0
	}
	var id int
	for _, c := range s[:idx] {
		if c >= '0' && c <= '9' {
			id = id*10 + int(c-'0')
		}
	}
	return id
}

// Convenience functions for each subsystem

// Editor emits an editor subsystem trace event (verbose level).
func Editor(event string, data any) {
	Emit("editor", event, LevelVerbose, data)
}

// EditorDetailed emits an editor subsystem trace event (detailed level).
func EditorDetailed(event string, data any) {
	Emit("editor", event, LevelDetailed, data)
}

// EditorHigh emits an editor subsystem trace event (high level).
func EditorHigh(event string, data any) {
	Emit("editor", event, LevelHigh, data)
}

// Agent emits an agent subsystem trace event (verbose level).
func Agent(event string, data any) {
	Emit("agent", event, LevelVerbose, data)
}

// AgentDetailed emits an agent subsystem trace event (detailed level).
func AgentDetailed(event string, data any) {
	Emit("agent", event, LevelDetailed, data)
}

// AgentHigh emits an agent subsystem trace event (high level).
func AgentHigh(event string, data any) {
	Emit("agent", event, LevelHigh, data)
}

// Shell emits a shell subsystem trace event (verbose level).
func Shell(event string, data any) {
	Emit("shell", event, LevelVerbose, data)
}

// ShellDetailed emits a shell subsystem trace event (detailed level).
func ShellDetailed(event string, data any) {
	Emit("shell", event, LevelDetailed, data)
}

// ShellHigh emits a shell subsystem trace event (high level).
func ShellHigh(event string, data any) {
	Emit("shell", event, LevelHigh, data)
}

// Parser emits a parser subsystem trace event (verbose level).
func Parser(event string, data any) {
	Emit("parser", event, LevelVerbose, data)
}

// ParserDetailed emits a parser subsystem trace event (detailed level).
func ParserDetailed(event string, data any) {
	Emit("parser", event, LevelDetailed, data)
}

// ParserHigh emits a parser subsystem trace event (high level).
func ParserHigh(event string, data any) {
	Emit("parser", event, LevelHigh, data)
}
