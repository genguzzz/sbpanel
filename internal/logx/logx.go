// Package logx builds the process loggers. Messages often carry text that comes from users,
// nodes or remote servers (names, error strings); line breaks inside one message are escaped so
// such text can't forge extra log lines.
package logx

import (
	"bytes"
	"io"
	"log"
)

// New is log.New(w, prefix, log.LstdFlags) with line breaks inside a message escaped.
func New(w io.Writer, prefix string) *log.Logger {
	return log.New(oneLine{w}, prefix, log.LstdFlags)
}

type oneLine struct{ w io.Writer }

// Write gets one complete message from log.Logger, ending in a single '\n'.
func (o oneLine) Write(p []byte) (int, error) {
	body := bytes.TrimSuffix(p, []byte("\n"))
	if bytes.IndexAny(body, "\r\n") < 0 {
		return o.w.Write(p)
	}
	esc := bytes.ReplaceAll(body, []byte("\r"), []byte(`\r`))
	esc = bytes.ReplaceAll(esc, []byte("\n"), []byte(`\n`))
	if _, err := o.w.Write(append(esc, '\n')); err != nil {
		return 0, err
	}
	return len(p), nil
}
