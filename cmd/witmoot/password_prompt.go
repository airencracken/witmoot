package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

func readPromptPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("password prompt requires a terminal; use --password-stdin")
	}
	return readConfirmedPassword(os.Stderr, func() ([]byte, error) {
		return term.ReadPassword(fd)
	})
}

func readConfirmedPassword(out io.Writer, read func() ([]byte, error)) (string, error) {
	password, err := readPromptLine(out, "Owner password: ", read)
	if err != nil {
		return "", err
	}
	confirmation, err := readPromptLine(out, "Confirm owner password: ", read)
	if err != nil {
		return "", err
	}
	if string(password) != string(confirmation) {
		return "", errors.New("passwords do not match")
	}
	return string(password), nil
}

func readPromptLine(out io.Writer, prompt string, read func() ([]byte, error)) ([]byte, error) {
	if _, err := fmt.Fprint(out, prompt); err != nil {
		return nil, err
	}
	password, err := read()
	if _, writeErr := fmt.Fprintln(out); err == nil && writeErr != nil {
		err = writeErr
	}
	if err != nil {
		return nil, err
	}
	return password, nil
}
