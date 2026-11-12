package main

import (
	"io"
	"os"

	"golang.org/x/term"
)

// enterScreen switches to the alternate screen in raw mode and returns a
// function that puts the terminal back.
func enterScreen() (func(), error) {
	restoreVT := enableVT()
	state, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		restoreVT()
		return nil, err
	}
	io.WriteString(os.Stdout, "\x1b[?1049h\x1b[?25l\x1b[2J")
	return func() {
		io.WriteString(os.Stdout, "\x1b[0m\x1b[2J\x1b[?25h\x1b[?1049l")
		term.Restore(int(os.Stdin.Fd()), state)
		restoreVT()
	}, nil
}
