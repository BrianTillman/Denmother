package cmd

import (
	"io"
	"os"

	"github.com/fatih/color"
)

func withDiscardedConsole(enabled bool, fn func() error) error {
	if !enabled {
		return fn()
	}

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	oldColorOutput := color.Output
	oldColorError := color.Error

	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}

	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, reader)
		close(done)
	}()

	os.Stdout = writer
	os.Stderr = writer
	color.Output = writer
	color.Error = writer

	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
		color.Output = oldColorOutput
		color.Error = oldColorError
		_ = writer.Close()
		_ = reader.Close()
		<-done
	}()

	return fn()
}
