// Package ui provides utilities for user interface elements, such as terminal colors and progress bars.
package ui

import (
	"fmt"
	"os"
	"sync/atomic"

	"github.com/mattn/go-isatty"
	"github.com/schollz/progressbar/v3"
)

// ProgressBarHelper manages the lifecycle of a CLI progress bar.
// It should be used to provide visual feedback for long-running operations.
type ProgressBarHelper struct {
	bar            *progressbar.ProgressBar
	total          int
	processedCount atomic.Int64 // Safely updated by concurrent goroutines
}

// NewProgressBarHelper creates and initializes a progress bar if stderr is a TTY.
// It takes the total number of items to be processed as an argument.
// Returns nil if no progress bar should be shown (e.g., if output is not a terminal).
func NewProgressBarHelper(totalItems int, descriptionPrefix string) *ProgressBarHelper {
	if !isatty.IsTerminal(os.Stderr.Fd()) {
		return nil // Don't show progress bar if not an interactive terminal
	}

	// Ensure descriptionPrefix has a trailing space if not empty
	if descriptionPrefix != "" && descriptionPrefix[len(descriptionPrefix)-1] != ' ' {
		descriptionPrefix += " "
	}

	bar := progressbar.NewOptions(totalItems,
		progressbar.OptionSetWriter(os.Stderr), // Write to stderr
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowCount(),     // Shows "X/Y"
		progressbar.OptionClearOnFinish(), // Clears the bar when done
		progressbar.OptionSetDescription(fmt.Sprintf("(%s0/%d nodes)", descriptionPrefix, totalItems)),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)
	return &ProgressBarHelper{bar: bar, total: totalItems}
}

// Increment advances the progress bar by one step and updates its description
// to show the current count of processed items.
func (p *ProgressBarHelper) Increment(descriptionPrefix string) {
	if p == nil || p.bar == nil {
		return
	}
	// Ensure descriptionPrefix has a trailing space if not empty
	if descriptionPrefix != "" && descriptionPrefix[len(descriptionPrefix)-1] != ' ' {
		descriptionPrefix += " "
	}

	processed := p.processedCount.Add(1) // Add(1) returns the new value
	p.bar.Describe(fmt.Sprintf("(%s%d/%d nodes)", descriptionPrefix, processed, p.total))
	p.bar.Add(1)
}

// Finish cleans up the progress bar. It should be called when the operation
// is complete or if an error occurs, to ensure the terminal is left in a clean state.
// It also prints a newline to ensure the cursor moves to the next line after the bar clears.
func (p *ProgressBarHelper) Finish() {
	if p == nil || p.bar == nil {
		return
	}
	// OptionClearOnFinish handles the actual clearing.
	// We explicitly call Clear here in case Finish is called before all items are processed
	// and OptionClearOnFinish might not trigger as expected.
	// p.bar.Clear() // This might be redundant with OptionClearOnFinish. Test behavior.
	fmt.Fprintln(os.Stderr) // Ensure cursor moves to next line after bar clears
}
