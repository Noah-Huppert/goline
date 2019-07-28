/*
usage: goline [-w] maxlen path ...

maxlen is the max line length to enforce, path ... is
a list of space seperated files to format.

Outputs GNU diff compatable transforms on files to ensure their lines are
no longer than the maxlen.

Nothing is done in cases where lines cannot be made shorter than maxlen.
Use the -w flag to see warnings about these lines.
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

func main() {
	args := os.Args[1:]

	if len(args) < 2 {
		log.Fatalf("usage: goline maxlen path ...\n")
	}

	maxlen, err := strconv.Atoi(args[0])
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
		// line is the current line being parsed. Lines are written to files
		// when newlines are encountered.
		// tok is the current token. Tokens cannot be split. See tokEnds for
		// runes which when encountered end a token.
		out := bytes.NewBuffer(nil)
		line := bytes.NewBuffer(nil)
		tok := bytes.NewBuffer(nil)

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
		//
		// Special characters which where not included:
		// underscore ('_'): variables can have in their name. We want to treat
		// variable names as 1 token.
		// back slash ('\\'): a backslash is used to escape something else,
		// we should treat a back slash and the thing it is escaping as 1 token.
		// double quote ('"') and single quote ('\''): a bit pointless to
		// consider any type of quote its own token. This would result in lines
		// which may contain an empty string and on the next line the
		// full string.
		tokEnds := []rune{'~', '!', '@', '#', '$', '%', '^', '&', '*', '(', ')',
			'-', '+', '=', '|', '}', ']', '{', '[', ':', ';', '/', '?', '.',
			'>', '<', ','}
		tokEndBytes := []byte{}
		for _, r := range tokEnds {
			tokEndBytes = append(tokEndBytes, byte(r))
		}

		// lastB is the last byte, empty if b is the first byte
		var lastB byte

		for { // For each byte
			b, err := srcBuf.ReadByte()
			if err == io.EOF {
				break
			}
			handleErr(err, "failed to read byte from source file \"%s\"", path)

			// If beginning of line indent write and record new indent level
			if lastB == byte('\t') && b == lastB {
				lineIndent.before.tabs++
				lastB = b
				continue
			} else if lastB == byte(' ') && b == lastB {
				lineIndent.before.spaces++
				lastB = b
				continue
			}

			// If entering comment
			if syntaxMode == "block-comment" {
				// Block comment mode requires special logic. It can only
				// be ended by '*/'. We must be careful not to enter go
				// or line-comment mode until we see this special block
				// comment end token.
				if lastB == byte('*') && b == byte('/') { // End comment
					syntaxMode = ""
					_ = tok.WriteByte(b)
					lastB = b
					continue
				}
			} else if lastB == byte('/') && b == byte('*') {
				// Start block comment
				syntaxMode = "block-comment"
				_ = tok.WriteByte(b)
				lastB = b
				continue
			} else if lastB == byte('/') && b == lastB {
				// Start line comment
				syntaxMode = "line-comment"
				_ = tok.WriteByte(b)
				lastB = b
				continue
			}

			// If token ending byte
			// doNewLine will force the code following this for loop
			// to add a new line to the file
			var doNewLine bool

			matchTokEndBytes := false

			for _, end := range tokEndBytes {
				if end == b { // Add tok to line
					matchTokEndBytes = true
					// Check if doing so would make line larger
					if line.Len()+tok.Len() > maxlen {
						// Token won't fit, newline and indent. See
						// syntaxMode for explanation of behavior.
						switch syntaxMode {
						case "line-comment":
							doNewLine = true
							break
						case "block-comment":
							doNewLine = true
							break
						default:
							doNewLine = true
							break
						}

						_, err = tok.WriteTo(out)
						handleErr(err, "failed to write token to line in "+
							"source file \"%s\"", path)
						tok.Reset()

						_ = tok.WriteByte(b)
					} else {
						// Token will fit on current line, add
						_, err = tok.WriteTo(out)
						handleErr(err, "failed to write token to line in "+
							"source file \"%s\"", path)
						tok.Reset()

						_ = tok.WriteByte(b)
					}
				}
			}

			// If new line
			if doNewLine || b == byte('\n') {
				// Add existing token to line
				_, err = tok.WriteTo(line)
				handleErr(err, "failed to write output for source file \"%s\"",
					path)
				tok.Reset()

				// Write new line to line
				_ = line.WriteByte(byte('\n'))

				// Write line to ouptut
				_, err = line.WriteTo(out)
				handleErr(err, "failed to write output for source file \"%s\"",
					path)
				line.Reset()
				continue
			}

			if !matchTokEndBytes {
				_ = tok.WriteByte(b)
			}

			lastB = b
		}

		// Write remaining lines and tokens
		if tok.Len() > 0 {
			_, err = tok.WriteTo(line)
			handleErr(err, "failed to write output for source file \"%s\"",
				path)
		}

		if line.Len() > 0 {
			_, err = line.WriteTo(out)
			handleErr(err, "failed to write output for source file \"%s\"",
				path)
		}

		log.Printf("%s\n", out.String())
	}
}
