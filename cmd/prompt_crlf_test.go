package cmd

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// Windows terminals end a line with "\r\n": the "\r" is not part of the
// answer. A choice by number, a choice by text, a default (an empty line) and
// text all read the same as with "\n".
func TestPrompterReadsCRLFAnswers(t *testing.T) {
	p := &prompter{in: bufio.NewReader(strings.NewReader("2\r\nacme\r\n\r\ninvoice-parser\r\ny\r\n")), out: &bytes.Buffer{}}
	items := []string{"acme", "default"}

	if i, err := p.choose("Workspace", items, 0); err != nil || i != 1 {
		t.Errorf(`"2\r\n" = %d, %v; want option 2`, i, err)
	}
	if i, err := p.choose("Workspace", items, 1); err != nil || i != 0 {
		t.Errorf(`"acme\r\n" = %d, %v; want acme`, i, err)
	}
	if i, err := p.choose("Workspace", items, 1); err != nil || i != 1 {
		t.Errorf(`"\r\n" = %d, %v; want the default`, i, err)
	}
	if s, err := p.text("Agent name", "x"); err != nil || s != "invoice-parser" {
		t.Errorf(`"invoice-parser\r\n" = %q, %v`, s, err)
	}
	if err := askToConfirm(destructiveAsk{prompt: "Overwrite [y/N]: ", accept: yesAnswer}, p); err != nil {
		t.Errorf(`"y\r\n" must confirm: %v`, err)
	}
}
