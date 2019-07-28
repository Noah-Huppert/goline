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

// indentParams records the number of spaces and tabs
type indentParams struct {
	// spaces is the number of spaces
	spaces uint

	// tabs is the number of tabs
	tabs uint
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

	maxlen, err := strconv.Atoi(args[1])
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

		// tokEnds is a set of runes that start new tokens
		tokEnds := []rune{'~', '!', '@', '#', '$', '%', '^', '&', '*', '(', ')',
			'-', '_', '+', '=', '\\', '|', '}', ']', '{', '[', ':', ';', '"',
			'\'', '/', '?', '.', '>', '<', ','}
		tokEndBytes := []byte{}
		for _, r := range tokEnds {
			tokEndBytes = append(tokEndByts, byte(r))
		}

		// indent is the number of tabs which were placed before current line
		var indent uint

		// syntaxMode is the context in which the current byte exists within
		// the code. Can be:
		//
		// "line-comment": Inside a comment on a single line. On line wrap
		// the indentation of the line before the wrap is used. New lines
		// wil also be prefixed with "// ".
		//
		// "block-comment": Inside a multi line block commnent. On line wrap
		// the indentation of the line before the wrap is used.
		//
		// "": Normal go code.
		var syntaxMode string

		// lastB is the last byte, nil if b is the first byte
		var lastB byte = nil

		for { // For each byte
			b, err := srcBuf.ReadByte()
			if err == io.EOF {
				break
			}
			handleErr(err, "failed to read byte from source file \"%s\"", path)

			// If beginning of line indent write and record new indent level
			if lastB == byte('\t') && b == byte('\t') {
				indent++
				_ = line.WriteByte(b)
				continue
			}

			// If tok is line comment start
			if tok.String() == "//" {
				syntaxMode = "line-comment"
				_ = tok.WriteByte(b)
				continue
			}

			// If token ending byte
			for _, end := range tokEndBytes {
				if end == b { // Add tok to line
					// Check if doing so would make line larger
					if line.Len()+tok.Len() > maxlen {
						// Token won't fit, newline and indent
					} else {
						// Token will fit on current line, add
						_, err = tok.WriteTo(out)
						handleErr(err, "failed to write token to line in "+
							"source file \"%s\"", path)
						tok.Reset()
					}
				}
			}

			if b == byte('\n') {
				_, err = tok.WriteTo(out)
				handleErr(err, "failed to write output for source file \"%s\"",
					path)
				tok.Reset()
			}
		}

		if tok.Len() > 0 {
			_, err = tok.WriteTo(out)
			handleErr(err, "failed to write output for source file \"%s\"", path)
		}

		log.Printf("%s\n", out.String())
	}
}
