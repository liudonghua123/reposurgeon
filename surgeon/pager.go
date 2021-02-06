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

// NewPager returnns a new pager instance.
func NewPager(ti *terminfo.Terminfo) (io.WriteCloser, error) {
	externalPager, err := NewExternalPager()
	if err != nil {
		return NewInternalPager(ti)
	}
	return externalPager, nil
}

// ExternalPager describes an outboard symbiont for paging documentatiomn.
type ExternalPager struct {
	w io.WriteCloser
	c chan error
	o sync.Once
}

// NewExternalPager returnns a new instance of an external pager such as more(1).
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

func (pager *ExternalPager) Write(p []byte) (n int, err error) {
	return pager.w.Write(p)
}

func (pager *ExternalPager) close() error {
	var err error
	pager.o.Do(func() {
		err = pager.w.Close()
	})
	return err
}

// Close finalizes an external pager
func (pager *ExternalPager) Close() error {
	pager.close()
	err := <-pager.c
	return err
}

// InternalPager uses no symbiont, going directly through terminfo
type InternalPager struct {
	h     int
	b     []byte
	lines chan []byte
	done  chan struct{}
}

// NewInternalPager returns a internal pager instances using terminfo.
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

func (pager *InternalPager) Write(b []byte) (n int, err error) {
	pager.b = append(pager.b, b...)
	lines := bytes.SplitAfter(pager.b, []byte("\n"))
	for _, line := range lines {
		if bytes.HasSuffix(line, []byte("\n")) {
			pager.lines <- line
		} else {
			pager.b = line
			return len(b) - len(pager.b), nil
		}
	}
	pager.b = []byte{}
	return len(b), nil
}

// WriteString ships a string to an external pager for viewing
func (pager *ExternalPager) WriteString(s string) (n int, err error) {
	return pager.Write([]byte(s))
}

// Close closes out an internal pager instance.
func (pager *InternalPager) Close() error {
	close(pager.lines)
	<-pager.done
	return nil
}

// WriteString ships a string to an internal pager for viewing
func (pager *InternalPager) WriteString(s string) (n int, err error) {
	return pager.Write([]byte(s))
}
