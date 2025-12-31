package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/olekukonko/tablewriter"
)

// Format represents output format types
type Format string

const (
	FormatText  Format = "text"
	FormatJSON  Format = "json"
	FormatTable Format = "table"
)

// CurrentFormat is the active output format
var CurrentFormat Format = FormatText

// SetFormat sets the output format
func SetFormat(f string) {
	switch strings.ToLower(f) {
	case "json":
		CurrentFormat = FormatJSON
	case "table":
		CurrentFormat = FormatTable
	default:
		CurrentFormat = FormatText
	}
}

// Print outputs a message to stdout
func Print(format string, args ...interface{}) {
	fmt.Printf(format, args...)
}

// Println outputs a message with newline to stdout
func Println(args ...interface{}) {
	fmt.Println(args...)
}

// Printf outputs a formatted message to stdout
func Printf(format string, args ...interface{}) {
	fmt.Printf(format, args...)
}

// PrintSuccess outputs a success message
func PrintSuccess(format string, args ...interface{}) {
	Success.Printf("✓ "+format+"\n", args...)
}

// PrintError outputs an error message to stderr
func PrintError(format string, args ...interface{}) {
	Error.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
}

// PrintWarning outputs a warning message
func PrintWarning(format string, args ...interface{}) {
	Warning.Printf("Warning: "+format+"\n", args...)
}

// PrintInfo outputs an info message
func PrintInfo(format string, args ...interface{}) {
	Info.Printf(format+"\n", args...)
}

// PrintVerbose outputs a message only in verbose mode
func PrintVerbose(verbose bool, format string, args ...interface{}) {
	if verbose {
		Dim.Printf(format+"\n", args...)
	}
}

// JSON outputs data as JSON
func JSON(data interface{}) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

// Table creates a new table writer
func Table(headers []string) *tablewriter.Table {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader(headers)
	table.SetBorder(false)
	table.SetHeaderLine(true)
	table.SetColumnSeparator("  ")
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	return table
}

// TableWithWriter creates a table with custom writer
func TableWithWriter(w io.Writer, headers []string) *tablewriter.Table {
	table := tablewriter.NewWriter(w)
	table.SetHeader(headers)
	table.SetBorder(false)
	table.SetHeaderLine(true)
	table.SetColumnSeparator("  ")
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	return table
}

// KeyValue prints a key-value pair
func KeyValue(key, value string) {
	Bold.Printf("%-15s", key+":")
	fmt.Printf(" %s\n", value)
}

// Header prints a section header
func Header(title string) {
	BoldCyan.Println(title)
	fmt.Println(strings.Repeat("-", len(title)))
}

// Divider prints a horizontal divider
func Divider() {
	Dim.Println(strings.Repeat("-", 40))
}

// ToJSON returns data as a pretty-printed JSON string
func ToJSON(data interface{}) (string, error) {
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// RenderTable renders a table to the given writer
func RenderTable(w io.Writer, headers []string, rows [][]string) {
	table := tablewriter.NewWriter(w)
	table.SetHeader(headers)
	table.SetBorder(false)
	table.SetHeaderLine(true)
	table.SetColumnSeparator("  ")
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.AppendBulk(rows)
	table.Render()
}

// StatusColor returns a color function based on status
func StatusColor(status string) func(a ...interface{}) string {
	switch strings.ToLower(status) {
	case "running", "deployed", "active", "healthy":
		return Success.SprintFunc()
	case "stopped", "pending", "building", "deploying":
		return Warning.SprintFunc()
	case "failed", "error", "unhealthy":
		return Error.SprintFunc()
	default:
		return fmt.Sprint
	}
}

// MaskSecret masks a secret value for display
func MaskSecret(value string) string {
	if len(value) <= 3 {
		return "***"
	}
	return value[:3] + "***"
}

// FormatDuration formats seconds into human readable duration
func FormatDuration(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%dh %dm", seconds/3600, (seconds%3600)/60)
}

// FormatBytes formats bytes into human readable size
func FormatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
