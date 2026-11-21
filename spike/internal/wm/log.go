package wm

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Logger writes every line to the console and to a log file next to the exe,
// so Christos can send the file back after a test.
type Logger struct {
	mu   sync.Mutex
	f    *os.File
	Path string
	t0   time.Time
}

// NewLogger opens waymark-spike-<role>-<time>.log beside the executable,
// or in the current folder if that is not writable.
func NewLogger(role string) *Logger {
	setupConsole()
	name := fmt.Sprintf("waymark-spike-%s-%s.log", role, time.Now().Format("20060102-150405"))
	dirs := []string{}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	dirs = append(dirs, ".", os.TempDir())
	l := &Logger{t0: time.Now()}
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if f, err := os.Create(p); err == nil {
			l.f, l.Path = f, p
			break
		}
	}
	return l
}

// Header records the environment of the test.
func (l *Logger) Header(role, version string) {
	u := "?"
	if cu, err := user.Current(); err == nil {
		u = cu.Username
	}
	host, _ := os.Hostname()
	l.Printf("waymark %s spike %s | %s/%s | host %s | user %s | admin: %s | %s",
		role, version, runtime.GOOS, runtime.GOARCH, host, u, IsAdmin(), OSVersion())
	for _, a := range InterfaceIPv4s() {
		l.Printf("interface %s %s", a[0], a[1])
	}
	if l.Path != "" {
		l.Printf("log file: %s", l.Path)
	}
}

// Printf prints a timestamped line to console and file.
func (l *Logger) Printf(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := fmt.Sprintf("%s  %s", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, a...))
	fmt.Println(line)
	if l.f != nil {
		fmt.Fprintln(l.f, line)
		l.f.Sync()
	}
}

// Result marks a line the test report needs.
func (l *Logger) Result(format string, a ...any) { l.Printf("RESULT "+format, a...) }

// Say prints to the console only (prompts and big displays).
func (l *Logger) Say(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Printf(format, a...)
}

// FileOnly writes to the log file only.
func (l *Logger) FileOnly(w func(io.Writer)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		w(l.f)
	}
}

func (l *Logger) Close() {
	if l.f != nil {
		l.f.Close()
	}
}

var stdin = bufio.NewReader(os.Stdin)

// Ask prints a prompt and reads one line.
func Ask(prompt string) string {
	fmt.Print(prompt)
	s, _ := stdin.ReadString('\n')
	return strings.TrimSpace(s)
}

// Lines feeds stdin lines into a channel (for interactive loops).
func Lines() <-chan string {
	ch := make(chan string)
	go func() {
		for {
			s, err := stdin.ReadString('\n')
			if err != nil {
				close(ch)
				return
			}
			ch <- strings.TrimSpace(s)
		}
	}()
	return ch
}

// WaitExit keeps a double-clicked console window open at the end.
func WaitExit() {
	Ask("\nEnter για έξοδο / Enter to exit ")
}
