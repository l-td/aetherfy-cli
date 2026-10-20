package output

import (
	"os"

	"github.com/fatih/color"
)

// Printer is a colour that prints where text for a person goes: stdout, or
// stderr in JSON mode (see mode.go). Everything else of color.Color (Sprintf,
// SprintFunc, Fprintf to a writer of your own) is unchanged.
type Printer struct{ *color.Color }

func newPrinter(attrs ...color.Attribute) *Printer { return &Printer{color.New(attrs...)} }

// Print writes like color.Color.Print, to the run's text destination.
func (p *Printer) Print(a ...interface{}) (int, error) { return p.Fprint(colorOut(), a...) }

// Printf writes like color.Color.Printf, to the run's text destination.
func (p *Printer) Printf(format string, a ...interface{}) (int, error) {
	return p.Fprintf(colorOut(), format, a...)
}

// Println writes like color.Color.Println, to the run's text destination.
func (p *Printer) Println(a ...interface{}) (int, error) { return p.Fprintln(colorOut(), a...) }

var (
	// Primary colors
	Green   = newPrinter(color.FgGreen)
	Red     = newPrinter(color.FgRed)
	Yellow  = newPrinter(color.FgYellow)
	Blue    = newPrinter(color.FgBlue)
	Cyan    = newPrinter(color.FgCyan)
	Magenta = newPrinter(color.FgMagenta)
	White   = newPrinter(color.FgWhite)
	Gray    = newPrinter(color.FgHiBlack)

	// Bold variants
	BoldGreen  = newPrinter(color.FgGreen, color.Bold)
	BoldRed    = newPrinter(color.FgRed, color.Bold)
	BoldYellow = newPrinter(color.FgYellow, color.Bold)
	BoldBlue   = newPrinter(color.FgBlue, color.Bold)
	BoldCyan   = newPrinter(color.FgCyan, color.Bold)
	BoldWhite  = newPrinter(color.FgWhite, color.Bold)

	// Semantic colors
	Success = Green
	Error   = Red
	Warning = Yellow
	Info    = Cyan
	Dim     = Gray
	Bold    = newPrinter(color.Bold)
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
