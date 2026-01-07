package gron

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/fatih/color"
	"github.com/nwidger/jsoncolor"
	"io"
	"sort"
)

// Exit codes
const (
	exitOK = iota
	exitOpenFile
	exitReadInput
	exitFormStatements
	exitFetchURL
	exitParseStatements
	exitJSONEncode
)

// Option bitfields
const (
	OptMonochrome = 1 << iota
	OptNoSort
	OptJSON
)

// Output colors
var (
	strColor   = color.New(color.FgYellow)
	braceColor = color.New(color.FgMagenta)
	bareColor  = color.New(color.FgBlue, color.Bold)
	numColor   = color.New(color.FgRed)
	boolColor  = color.New(color.FgCyan)
)

// gronVersion stores the current gron version, set at build
// time with the ldflags -X option
var gronVersion = "dev"

// an actionFn represents a main action of the program, it accepts
// an input, output and a bitfield of options; returning an exit
// code and any error that occurred
type actionFn func(io.Reader, io.Writer, int) (int, error)

// Gron is the default action. Given JSON as the input it returns a list
// of assignment Statements. Possible options are OptNoSort and OptMonochrome
// Gron transforms a.json => a.kv
func Gron(r io.Reader, opts int) ([]Statement, error) {
	var err error

	ss, err := statementsFromJSON(r, Statement{{"json", TypBare}})
	if err != nil {
		return nil, fmt.Errorf("failed to form Statements: %s", err)
	}

	// Go's maps do not have well-defined ordering, but we want a consistent
	// output for a given input, so we must sort the Statements
	if opts&OptNoSort == 0 {
		sort.Sort(ss)
	}

	return ss, nil
}

func Simplify(stmts []Statement) []string {
	var ret []string
	for _, stmt := range stmts {
		item := stmt.Simplify()
		if len(item) > 0 {
			ret = append(ret, item)
		}
	}
	return ret
}

// gronStream is like the gron action, but it treats the input as one
// JSON object per line. There's a bit of code duplication from the
// gron action, but it'd be fairly messy to combine the two actions
func gronStream(r io.Reader, w io.Writer, opts int) (int, error) {
	var err error
	errstr := "failed to form Statements"
	var i int
	var sc *bufio.Scanner
	var buf []byte

	var conv func(s Statement) string
	if opts&OptMonochrome > 0 {
		conv = statementToString
	} else {
		conv = statementToColorString
	}

	// Helper function to make the prefix Statements for each line
	makePrefix := func(index int) Statement {
		return Statement{
			{"json", TypBare},
			{"[", TypLBrace},
			{fmt.Sprintf("%d", index), TypNumericKey},
			{"]", TypRBrace},
		}
	}

	// The first line of output needs to establish that the top-level
	// thing is actually an array...
	top := Statement{
		{"json", TypBare},
		{"=", TypEquals},
		{"[]", TypEmptyArray},
		{";", TypSemi},
	}

	if opts&OptJSON > 0 {
		top, err = top.jsonify()
		if err != nil {
			goto out
		}
	}

	fmt.Fprintln(w, conv(top))

	// Read the input line by line
	sc = bufio.NewScanner(r)
	buf = make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	i = 0
	for sc.Scan() {

		line := bytes.NewBuffer(sc.Bytes())

		var ss Statements
		ss, err = statementsFromJSON(line, makePrefix(i))
		i++
		if err != nil {
			goto out
		}

		// Go's maps do not have well-defined ordering, but we want a consistent
		// output for a given input, so we must sort the Statements
		if opts&OptNoSort == 0 {
			sort.Sort(ss)
		}

		for _, s := range ss {
			if opts&OptJSON > 0 {
				s, err = s.jsonify()
				if err != nil {
					goto out
				}

			}
			fmt.Fprintln(w, conv(s))
		}
	}
	if err = sc.Err(); err != nil {
		errstr = "error reading multiline input: %s"
	}

out:
	if err != nil {
		return exitFormStatements, fmt.Errorf(errstr+": %s", err)
	}
	return exitOK, nil

}

func colorizeJSON(src []byte) ([]byte, error) {
	out := &bytes.Buffer{}
	f := jsoncolor.NewFormatter()

	f.StringColor = strColor
	f.ObjectColor = braceColor
	f.ArrayColor = braceColor
	f.FieldColor = bareColor
	f.NumberColor = numColor
	f.TrueColor = boolColor
	f.FalseColor = boolColor
	f.NullColor = boolColor

	err := f.Format(out, src)
	if err != nil {
		return out.Bytes(), err
	}
	return out.Bytes(), nil
}
