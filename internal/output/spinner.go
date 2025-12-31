package output

import (
	"time"

	"github.com/briandowns/spinner"
)

// Spinner wraps the spinner library
type Spinner struct {
	s *spinner.Spinner
}

// NewSpinner creates a new spinner with a message
func NewSpinner(message string) *Spinner {
	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " " + message
	return &Spinner{s: s}
}

// Start begins the spinner animation
func (sp *Spinner) Start() {
	sp.s.Start()
}

// Stop stops the spinner
func (sp *Spinner) Stop() {
	sp.s.Stop()
}

// Success stops spinner with success message
func (sp *Spinner) Success(message string) {
	sp.s.Stop()
	PrintSuccess(message)
}

// Fail stops spinner with error message
func (sp *Spinner) Fail(message string) {
	sp.s.Stop()
	PrintError(message)
}

// UpdateMessage changes the spinner message
func (sp *Spinner) UpdateMessage(message string) {
	sp.s.Suffix = " " + message
}

// WithSpinner runs a function with a spinner
func WithSpinner(message string, fn func() error) error {
	sp := NewSpinner(message)
	sp.Start()
	err := fn()
	sp.Stop()
	return err
}
