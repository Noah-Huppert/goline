/*
usage: goline [-w] maxlen path ...

maxlen is the max line length to enforce, path ... is
a list of space seperated files to format.

Outputs GNU diff compatable transforms on files to ensure their lines are
no longer than the maxlen.

Nothing is done in cases where lines cannot be made shorter than maxlen.
Use the -w flag to see warnings about these lines.

Design:

Each input file's input is processed 1 byte at a time.
*/
package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
)

// handleErr prints and exits if there is an error
func handleErr(err error, msg string, a ...interface{}) {
	if err == nil {
		return
	}

	log.Fatalf("%s: %s\n", fmt.Sprintf(msg, a), err.Error())
}

// indent records the indenation of line.
// Tracks spaces and tabs before and after content.
// Content is considered any non space or tab character.
type indent struct {
	// before is the indentation before content
	before indentParams

	// after is the indentation after a pattern
	after map[indentPattern]indentParams
}

// indentParams records the number of spaces and tabs.
// If spaces and tabs are specifies it is assumed that tabs
// came before spaces.
type indentParams struct {
	// spaces is the number of spaces
	spaces uint

	// tabs is the number of tabs
	tabs uint
}

// bytes returns a byte array representing the spacing
func (p indentParams) bytes() []byte {
	o := []byte{}

	for _, v := range p.tabsBytes() {
		o = append(o, v)
	}

	for _, v := range p.spacesBytes() {
		o = append(o, v)
	}

	return o
}

// spacesBytes returns a byte array full of spaces of size .spaces
func (p indentParams) spacesBytes() []byte {
	o := []byte{}
	for i := uint(0); i < p.spaces; i++ {
		o = append(o, byte(' '))
	}
	return o
}

// tabsBytes returns a byte array full of tabs of size .tabs
func (p indentParams) tabsBytes() []byte {
	o := []byte{}
	for i := uint(0); i < p.tabs; i++ {
		o = append(o, byte('\t'))
	}
	return o
}

// indentPattern specifies a location on a line indepentant of the current
// indentation (ie., it doesn't use characters as a measurement because
// those could change with new indentation).
type indentPattern struct {
	// patern which matches the beginning of the location
	pattern string

	// matchIndex specifies which match of pattern on the line to use for
	// the location. This is used to handle cases where there is more than
	// one occurrence of the pattern on a line.
	matchIndex uint
}

// maxlenWriter writes lines no longer than maxlen to an output writer.
// Close() must be called to flush bytes out of buffers.
type maxlenWriter struct {
	// maxlen is the maximum number of characters a line can be
	maxlen uint64

	// out is the buffer to which lines will be written
	out *bytes.Buffer

	// line holds the current line which is under maxlen
	line *bytes.Buffer
}

// newMaxlenWriter creates a new maxlenWriter
func newMaxlenWriter(maxlen uint64, out *bytes.Buffer) *maxlenWriter {
	return &maxlenWriter{
		maxlen: maxlen,
		out:    out,
		line:   bytes.NewBuffer(nil),
	}
}

// Write writes tok to line if result is less than maxlen long. If line
// would be too long with tok then line is written to out and then
// the tok are written to a line.
func (w *maxlenWriter) Write(tok *bytes.Buffer, syntaxMode string,
	lineIndent indent) error {

	// If we need a new line
	if w.line.Len()+tok.Len() > int(w.maxlen) {
		// handle new lines different based on syntaxMode
		switch syntaxMode {
		default:
			lineIndent.before.tabs++

			w.line.WriteByte(byte('\n'))
			break
		}

		// Write line to out buffer
		_, err := w.line.WriteTo(w.out)
		if err != nil {
			return fmt.Errorf("failed to write line to out: %s", err.Error())
		}

		log.Printf("out+%s", w.line.String())

		w.line.Reset()

		// handle writing new line pre-content based on syntaxMode
		switch syntaxMode {
		default:
			_, err := w.line.Write(lineIndent.before.bytes())
			if err != nil {
				return fmt.Errorf("failed to write indentation before "+
					"line: %s", err.Error())
			}
			break
		}
	}

	// TODO: figure out why tok is empty he
	_, err := tok.WriteTo(w.line)
	if err != nil {
		return fmt.Errorf("failed to write to line: %s", err.Error())
	}

	return nil
}

// Close flushes buffers
func (w *maxlenWriter) Close() error {
	// Flush line
	if w.line.Len() > 0 {
		_, err := w.line.WriteTo(w.out)
		if err != nil {
			return fmt.Errorf("failed to flush line to out buffer: %s",
				err.Error())
		}
	}

	return nil
}

func main() {
	args := os.Args[1:]

	if len(args) < 2 {
		log.Fatalf("usage: goline maxlen path ...\n")
	}

	maxlen, err := strconv.ParseUint(args[0], 10, 64)
	handleErr(err, "failed to parse maxlen \"%s\" argument as int", args[1])

	paths := args[1:]

	for _, path := range paths {
		// Open file
		absPath, err := filepath.Abs(path)
		handleErr(err, "failed to get absolute path to file \"%s\"", path)

		srcF, err := os.Open(filepath.Clean(absPath))
		handleErr(err, "failed to open source file \"%s\"", path)

		srcBuf := bytes.NewBuffer(nil)
		_, err = srcBuf.ReadFrom(srcF)
		handleErr(err, "failed to read source file contents \"%s\"", path)

		// Parse file and enforce maxlen.
		// out is the maxlen enforced file.
		// tok is the current token. Tokens cannot be split. See tokEnds for
		// runes which when encountered end a token.
		out := bytes.NewBuffer(nil)
		maxlenW := newMaxlenWriter(maxlen, out)
		lineNum := 0
		tok := bytes.NewBuffer(nil)

		defer func() {
			err := maxlenW.Close()
			handleErr(err, "failed to maxlenWriter buffer for \"%s\"", path)
		}()

		// lineIndent is the current line's indent
		var lineIndent indent

		// syntaxMode is the context in which the current byte exists within
		// the code. Can be:
		//
		// "line-comment": Inside a comment on a single line. If a line must be
		// wrapped the indentation of the line before the wrap is used. New lines
		// will also be prefixed with "//". This mode ends when the a new line
		// is started.
		//
		// "block-comment": Inside a multi line block commnent. On line wrap
		// the indentation of the line before the wrap is used. This line ends
		// when the '*/' end token is seen.
		//
		// "": Normal go code. Tries to follow gofmt spec. This mode ends when
		// any of the above modes decides to begin. When the above modes end
		// they switch back to this mode.
		var syntaxMode string

		// tokEnds is a set of runes that start new tokens.
		///
		// It should be noted that detecting tokens for syntaxMode occurs
		// first. So if a line-comment starts (which includes 2 tokEnds
		// runes in a row: '//') a new token will not be started since
		// special logic handles syntaxMode changes.
		tokEnds := []rune{'~', '!', '^', '*', '(', ')', '-', '+', '=', '|',
			'}', '{', ':', ';', '/', '?', '.', '>', '<', ',', '\n'}
		tokEndBytes := []byte{}
		for _, r := range tokEnds {
			tokEndBytes = append(tokEndBytes, byte(r))
		}

		// lastB is the last byte, empty if b is the first byte
		var lastB byte

		for {
			// For each byte
			b, err := srcBuf.ReadByte()
			if err == io.EOF {
				break
			}
			handleErr(err, "failed to read byte from source file \"%s\"", path)

			// If beginning of line indent write and record new indent level
			if b == byte('\t') {
				lineIndent.before.tabs++
			} else if lastB == byte(' ') && b == lastB {
				lineIndent.before.spaces++
			}

			// If entering comment
			if syntaxMode == "block-comment" {
				// Block comment mode requires special logic. It can only
				// be ended by '*/'. We must be careful not to enter go
				// or line-comment mode until we see this special block
				// comment end token.
				if lastB == byte('*') && b == byte('/') { // End comment
					syntaxMode = ""
				}
			} else if lastB == byte('/') && b == byte('*') {
				// Start block comment
				syntaxMode = "block-comment"
			} else if lastB == byte('/') && b == lastB {
				// Start line comment
				syntaxMode = "line-comment"
			}

			// Accumulate bytes in tok
			/*
				switch b {
				case byte('\n'):
					tok.WriteByte(byte('\n'))
					lineNum++

					err := maxlenW.Write(tok, syntaxMode, lineIndent)
					handleErr(err, "failed to write last token on line %d \"%s\"",
						lineNum, tok.String())
					tok.Reset()
					break
				default:
			*/
			// If b starts a new token
			matchTokEnds := false
			for _, v := range tokEndBytes {
				if v == b {
					matchTokEnds = true
					break
				}
			}

			// Write existing token to line and start new token
			if matchTokEnds {
				err = maxlenW.Write(tok, syntaxMode, lineIndent)
				handleErr(err, "failed to write token \"%s\" to line %d",
					tok.String(), lineNum)

				tok.Reset()
			}

			tok.WriteByte(b)
			/*break
			}*/

			lastB = b
		}

		// Write remaining lines and tokens
		if tok.Len() > 0 {
			err = maxlenW.Write(tok, syntaxMode, lineIndent)
			handleErr(err, "failed to write output for source file \"%s\"",
				path)
		}

		fmt.Printf("%s\n", out.String())
	}
}
