package main

import (
	"fmt"
	"os"
	"sync/atomic"

	"github.com/mattn/go-isatty"
	"github.com/schollz/progressbar/v3"
)

// progressBarHelper manages the lifecycle of the progress bar.
type progressBarHelper struct {
	bar            *progressbar.ProgressBar
	total          int
	processedCount atomic.Int64 // Use atomic for safe concurrent updates
}

// newProgressBarHelper creates and initializes a progress bar if stderr is a TTY.
// Returns nil if no progress bar should be shown.
func newProgressBarHelper(totalItems int) *progressBarHelper {
	if !isatty.IsTerminal(os.Stderr.Fd()) {
		return nil // Don't show progress bar if not an interactive terminal
	}

	bar := progressbar.NewOptions(totalItems,
		progressbar.OptionSetWriter(os.Stderr), // Write to stderr
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowCount(),     // Shows "X/Y"
		progressbar.OptionClearOnFinish(), // Clears the bar when done
		progressbar.OptionSetDescription(fmt.Sprintf("(listing pods on 0/%d nodes)", totalItems)),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)
	return &progressBarHelper{bar: bar, total: totalItems}
}

// Increment advances the progress bar and updates its description.
func (p *progressBarHelper) Increment() {
	if p == nil || p.bar == nil {
		return
	}
	processed := p.processedCount.Add(1) // Add(1) returns the new value
	p.bar.Describe(fmt.Sprintf("(listing pods on %d/%d nodes)", processed, p.total))
	p.bar.Add(1)
}

// Finish ensures the progress bar is cleaned up and a newline is printed.
func (p *progressBarHelper) Finish() {
	if p == nil || p.bar == nil {
		return
	}
	// OptionClearOnFinish handles the actual clearing.
	fmt.Fprintln(os.Stderr) // Ensure cursor moves to next line after bar clears
}
