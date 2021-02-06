package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	terminfo "github.com/xo/terminfo"
	terminal "golang.org/x/crypto/ssh/terminal"
)

func NewPager(ti *terminfo.Terminfo) (io.WriteCloser, error) {
	externalPager, err := NewExternalPager()
	if err != nil {
		return NewInternalPager(ti)
	} else {
		return externalPager, nil
	}
}

type ExternalPager struct {
	w io.WriteCloser
	c chan error
	o sync.Once
}

func NewExternalPager() (*ExternalPager, error) {
	cmd := os.Getenv("PAGER")
	if cmd == "" {
		cmd = "more"
	}
	_, err := exec.LookPath(cmd)
	if err != nil {
		return nil, err
	}
	externalPager := exec.Command(cmd)
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	externalPager.Stdin = r
	externalPager.Stdout = os.Stdout
	externalPager.Stderr = os.Stderr
	c := make(chan error)
	writer := ExternalPager{
		w: w,
		c: c,
		o: sync.Once{},
	}

	go func() {
		defer func() {
			close(c)
			writer.close()
		}()
		writer.c <- externalPager.Run()
	}()

	return &writer, nil
}

func (w *ExternalPager) Write(p []byte) (n int, err error) {
	return w.w.Write(p)
}

func (w *ExternalPager) close() error {
	var err error
	w.o.Do(func() {
		err = w.w.Close()
	})
	return err
}

func (w *ExternalPager) Close() error {
	w.close()
	err := <-w.c
	return err
}

type InternalPager struct {
	h     int
	b     []byte
	lines chan []byte
	done  chan struct{}
}

func NewInternalPager(ti *terminfo.Terminfo) (io.WriteCloser, error) {
	_, height, err := terminal.GetSize(0)
	if err != nil {
		return nil, err
	}
	writer := InternalPager{
		h:     height,
		b:     make([]byte, 0),
		lines: make(chan []byte, 1000),
		done:  make(chan struct{}, 1),
	}
	go func() {
		defer func() {
			close(writer.done)
		}()
		for {
			for i := 0; i < writer.h-1; i++ {
				line, ok := <-writer.lines
				if !ok {
					return
				}
				os.Stdout.Write(line)
			}
			ti.Fprintf(os.Stdout, terminfo.EnterReverseMode)
			os.Stdout.WriteString("-- Press Enter for more--")
			ti.Fprintf(os.Stdout, terminfo.ExitAttributeMode)
			fmt.Scanln()
			ti.Fprintf(os.Stdout, terminfo.CursorUp)
			ti.Fprintf(os.Stdout, terminfo.ClrEol)
		}
	}()

	return &writer, nil
}

func (self *InternalPager) Write(b []byte) (n int, err error) {
	self.b = append(self.b, b...)
	lines := bytes.SplitAfter(self.b, []byte("\n"))
	for _, line := range lines {
		if bytes.HasSuffix(line, []byte("\n")) {
			self.lines <- line
		} else {
			self.b = line
			return len(b) - len(self.b), nil
		}
	}
	self.b = []byte{}
	return len(b), nil
}

func (self *ExternalPager) WriteString(s string) (n int, err error) {
	return self.Write([]byte(s))
}

func (self *InternalPager) Close() error {
	close(self.lines)
	<-self.done
	return nil
}

func (self *InternalPager) WriteString(s string) (n int, err error) {
	return self.Write([]byte(s))
}
