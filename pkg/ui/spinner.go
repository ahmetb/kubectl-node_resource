// Copyright 2025 Ahmet Alp Balkan
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

var defaultSpinnerChars = []string{"|", "/", "-", "\\"}

// Spinner represents an animated progress indicator.
type Spinner struct {
	mu         sync.Mutex
	chars      []string
	delay      time.Duration
	writer     io.Writer
	message    string
	stopChan   chan struct{}
	running    bool
	lineLength int // To store the length of the last printed line for clearing
}

// NewSpinner creates a new Spinner instance.
// Default characters are "|", "/", "-", "\".
// Default delay is 100ms.
// Default writer is os.Stdout.
func NewSpinner(options ...Option) *Spinner {
	s := &Spinner{
		chars:    defaultSpinnerChars,
		delay:    100 * time.Millisecond,
		writer:   os.Stdout,
		stopChan: make(chan struct{}),
	}
	for _, opt := range options {
		opt(s)
	}
	return s
}

// Option is a functional option type for Spinner.
type Option func(*Spinner)

// WithChars sets the characters for the spinner animation.
func WithChars(chars []string) Option {
	return func(s *Spinner) {
		if len(chars) > 0 {
			s.chars = chars
		}
	}
}

// WithDelay sets the delay between animation frames.
func WithDelay(delay time.Duration) Option {
	return func(s *Spinner) {
		s.delay = delay
	}
}

// WithWriter sets the output writer for the spinner.
func WithWriter(writer io.Writer) Option {
	return func(s *Spinner) {
		s.writer = writer
	}
}

// Start begins the spinner animation with the given message.
func (s *Spinner) Start(message string) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.message = message
	s.stopChan = make(chan struct{}) // Recreate stopChan in case Start is called after Stop
	s.mu.Unlock()

	go func() {
		for i := 0; ; i++ {
			select {
			case <-s.stopChan:
				return
			default:
				s.mu.Lock()
				if !s.running { // Double check after acquiring lock
					s.mu.Unlock()
					return
				}
				output := fmt.Sprintf("\r%s %s ", s.chars[i%len(s.chars)], s.message)
				s.lineLength = len(output) - 1 // -1 for \r
				fmt.Fprint(s.writer, output)
				s.mu.Unlock()
				time.Sleep(s.delay)
			}
		}
	}()
}

// Stop halts the spinner animation and clears the line.
func (s *Spinner) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}
	s.running = false
	close(s.stopChan) // Signal the animation goroutine to stop

	// Clear the line
	// We subtract 1 from lineLength because \r itself is not counted in visible characters.
	// However, the original calculation of lineLength already accounts for \r by subtracting 1.
	// The issue might be if message is empty or very short.
	// Let's ensure lineLength is at least the length of the spinner char + space.
	clearLength := s.lineLength
	if clearLength < len(s.chars[0])+1 { // spinner char + space
		clearLength = len(s.chars[0]) + 1
	}
	if clearLength < len(s.message)+len(s.chars[0])+1 { // spinner + space + message
		clearLength = len(s.message) + len(s.chars[0]) + 1
	}

	fmt.Fprintf(s.writer, "\r%s\r", strings.Repeat(" ", clearLength))
}

// RunWithSpinner executes the given action function while displaying a spinner.
// It starts the spinner with the provided message, runs the action,
// stops the spinner, and returns the error from the action.
func RunWithSpinner(message string, action func() error, writer io.Writer) error {
	// Ensure writer is not nil, default to os.Stdout
	outWriter := writer
	if outWriter == nil {
		outWriter = os.Stdout
	}

	// Do not show spinner if not a TTY (e.g. when piping output to a file)
	if f, ok := outWriter.(*os.File); !ok || !isTerminal(f.Fd()) {
		return action()
	}

	s := NewSpinner(WithWriter(outWriter))
	s.Start(message)
	err := action()
	s.Stop()
	return err
}

// isTerminal checks if the given file descriptor is a terminal.
// This is a simplified check. For more robust checks, consider platform-specific code
// or a library like "golang.org/x/term".
func isTerminal(fd uintptr) bool {
	// This is a very basic check. A more robust solution would use
	// golang.org/x/term or platform-specific ioctls.
	// For now, we assume if it's os.Stdout or os.Stderr, it might be a terminal.
	// This check is often problematic and might need to be refined or made configurable.
	// A common approach is to check if `os.Stdout.Stat()` has `os.ModeCharDevice`.
	if term := os.Getenv("TERM"); term != "" && term != "dumb" {
		if f, ok := os.Stdout.Stat(); ok == nil {
			return (f.Mode() & os.ModeCharDevice) == os.ModeCharDevice
		}
	}
	return false // Default to false if unsure, to avoid messing up non-terminal output
}
