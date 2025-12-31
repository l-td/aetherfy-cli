package output

import (
	"os"

	"github.com/fatih/color"
)

// ColorPrinter is an alias for color.Color for external use
type ColorPrinter = color.Color

var (
	// Primary colors
	Green   = color.New(color.FgGreen)
	Red     = color.New(color.FgRed)
	Yellow  = color.New(color.FgYellow)
	Blue    = color.New(color.FgBlue)
	Cyan    = color.New(color.FgCyan)
	Magenta = color.New(color.FgMagenta)
	White   = color.New(color.FgWhite)
	Gray    = color.New(color.FgHiBlack)

	// Bold variants
	BoldGreen  = color.New(color.FgGreen, color.Bold)
	BoldRed    = color.New(color.FgRed, color.Bold)
	BoldYellow = color.New(color.FgYellow, color.Bold)
	BoldBlue   = color.New(color.FgBlue, color.Bold)
	BoldCyan   = color.New(color.FgCyan, color.Bold)
	BoldWhite  = color.New(color.FgWhite, color.Bold)

	// Semantic colors
	Success = Green
	Error   = Red
	Warning = Yellow
	Info    = Cyan
	Dim     = Gray
	Bold    = color.New(color.Bold)
)

// DisableColors turns off all color output
func DisableColors() {
	color.NoColor = true
}

// EnableColors turns on color output
func EnableColors() {
	color.NoColor = false
}

// init checks for NO_COLOR environment variable
func init() {
	if os.Getenv("NO_COLOR") != "" {
		DisableColors()
	}
}
