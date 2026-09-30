package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	// ellipsis marks the place where a line was cut. It is three real dots and
	// not the single ellipsis character, because a real dot is one column wide
	// in every terminal, while the ellipsis character is one column in some
	// and two in others.
	ellipsis = "..."

	// resetAllModes is ESC [ 0 m, which returns every text attribute to its
	// default. It is added after a cut, because the cut may have removed the
	// reset sequence that the input carried. Without it, a colour or a bold
	// mode would stay switched on for all later output.
	resetAllModes = "\x1b[0m"
)

// TerminalEnv holds the facts about the environment that the program runs in.
// It is filled one time at the start. The fields are facts only. The decision
// about what those facts mean belongs to main.
//
// The terminal size is not here on purpose. The size changes when the person
// resizes the window, so it is read inside the main loop instead.
type TerminalEnv struct {
	// StdinIsTerminal is true when standard input is a terminal. For this
	// program that means no data was piped in, because the normal use is
	// "other-program | collapse".
	StdinIsTerminal bool

	// StdoutIsTerminal is true when standard output is a terminal. When it is
	// false, the output goes into a file or into another program, and no
	// person watches it.
	StdoutIsTerminal bool

	// SupportsANSI is true when the output accepts escape sequences for cursor
	// movement. Without them one line cannot be written over another.
	SupportsANSI bool

	// Term is the value of the TERM environment variable. It is kept so that a
	// refusal to run can explain itself.
	Term string
}

func main() {
	flag.Usage = printUsage
	flag.Parse()

	patterns, err := patternsAfterSeparator(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "collapse:", err)
		flag.Usage()
		os.Exit(2)
	}

	termEnv := DetectTerminalEnv()

	if termEnv.StdinIsTerminal {
		fmt.Fprintln(os.Stderr, "collapse: no input. Send the output of "+
			"another program into this one, for example: terraform apply | collapse")
		os.Exit(1)
	}

	if !termEnv.StdoutIsTerminal {
		fmt.Fprintln(os.Stderr, "collapse: the output is not a terminal. This "+
			"program collapses lines on a screen, so it has nothing to do here")
		os.Exit(1)
	}

	if !termEnv.SupportsANSI {
		fmt.Fprintf(os.Stderr, "collapse: this terminal does not accept escape "+
			"sequences (TERM=%q). Lines cannot be written over\n", termEnv.Term)
		os.Exit(1)
	}

	//TODO: Need to move this into the line by line loop because if user resizes
	//term window while app is running - output will start to break.
	//TODO: handle err
	termWidth, _, _ := term.GetSize(int(os.Stdout.Fd()))

	scanner := bufio.NewScanner(os.Stdin)

	// Flag var that is used to clean current line if previous line was marked as
	// not worthy of beeing kept (contains pattern that we dont care about and do
	// not contain long output)
	prevLineWasIrrelevant := true
	for scanner.Scan() {
		line := scanner.Text()

		thisLineIsIrrelevant := containsAnyPattern(line, patterns)

		// Only a line that may be written over has to be cut. Such a line must
		// stay on one row: if it wrapped, \r and \x1b[2K would act on the last
		// row alone, and the rows above it would stay on the screen for ever.
		//
		// A line that is kept is not cut, because it holds the text that the
		// person wants to read. It may wrap, and that is normal.
		//
		// One column is left free, because many terminals wrap late. A
		// character written into the last column leaves the cursor in a waiting
		// state, and the next character then causes the wrap.
		output := line
		if thisLineIsIrrelevant {
			output = truncateMiddle(line, termWidth-1)
		}

		if prevLineWasIrrelevant && thisLineIsIrrelevant {
			// \r - return cursor to begining of line
			// \x1b[2K - Same as ESC [ 2 k, which is ANSI way of saying "clean line from cursor till end of line"
			fmt.Print("\r\x1b[2K")
		} else {
			fmt.Print("\n")
		}

		prevLineWasIrrelevant = thisLineIsIrrelevant

		fmt.Print(output)
	}

	//TODO: fix all errors handling is some unified and neat way
	if err := scanner.Err(); err != nil {
	}
}

// printUsage explains how to run the program. The flag package calls it for the
// -h option, and for an option that it does not know.
func printUsage() {
	fmt.Fprint(os.Stderr, `
	Usage: collapse -- SUBSTRING [SUBSTRING ...]

	Reads lines from standard input and writes them to the terminal. A line that
	holds any of the given substrings is written over the line before it, when that
	line held one as well. Matching is plain text, not a regular expression.

	Every substring goes after the -- separator.

	Example:
	  terraform apply | collapse -- 'Refreshing state' 'Still creating'
	`)
}

// patternsAfterSeparator returns the substrings that stand after the "--"
// separator. The separator is always needed. One rule then covers every
// substring, including a substring that starts with a dash.
func patternsAfterSeparator(args []string) ([]string, error) {
	separatorIndex := -1
	for index, arg := range args {
		if arg == "--" {
			separatorIndex = index
			break
		}
	}

	if separatorIndex < 0 {
		return nil, errors.New(`missing "--" separator. Every substring goes after it`)
	}

	patterns := args[separatorIndex+1:]
	if len(patterns) == 0 {
		return nil, errors.New(`no substring after "--". Give at least one`)
	}

	return patterns, nil
}

// DetectTerminalEnv examines the environment and reports what the program can
// do. It never fails. An environment that does not work is described by the
// fields, and main decides what to do about it.
func DetectTerminalEnv() TerminalEnv {
	termEnv := TerminalEnv{}

	termEnv.Term = os.Getenv("TERM")
	termEnv.StdinIsTerminal = term.IsTerminal(int(os.Stdin.Fd()))
	termEnv.StdoutIsTerminal = term.IsTerminal(int(os.Stdout.Fd()))

	// This field comes last, because supportsANSI reads the fields above it.
	termEnv.SupportsANSI = supportsANSI(termEnv)

	return termEnv
}

// supportsANSI decides whether the output accepts escape sequences. A terminal
// cannot be asked this directly, so the test uses the environment variables
// that are conventional for the purpose.
func supportsANSI(termEnv TerminalEnv) bool {
	if !termEnv.StdoutIsTerminal {
		return false
	}

	// TERM=dumb is the conventional value for a terminal with no features.
	// Emacs shell buffers and some build systems set it. An empty TERM means
	// that nothing described the terminal at all.
	if termEnv.Term == "" || termEnv.Term == "dumb" {
		return false
	}

	// A build system stores the output as a file and shows it later, so a
	// redraw would leave escape sequences in the stored log.
	if os.Getenv("CI") != "" {
		return false
	}

	if runtime.GOOS == "windows" {
		return windowsSupportsANSI()
	}

	return true
}

// windowsSupportsANSI reports whether the Windows console accepts escape
// sequences. Windows Terminal and the ANSICON tool announce themselves with an
// environment variable. The older console needs
// ENABLE_VIRTUAL_TERMINAL_PROCESSING, which this program does not switch on.
func windowsSupportsANSI() bool {
	if os.Getenv("WT_SESSION") != "" {
		return true
	}
	if os.Getenv("ANSICON") != "" {
		return true
	}
	return false
}

// containsAnyPattern reports whether line holds at least one of the substrings.
func containsAnyPattern(line string, patterns []string) bool {
	//TODO: possible cost. This scans the whole line one time for each pattern,
	//so the work is len(patterns) * len(line) for every input line. With a few
	//short patterns it is cheap, because strings.Contains uses tuned assembly.
	//With many patterns it becomes the hot path.
	//Ways to make it faster, in order of effort:
	//1. Put the pattern that matches most often first. The loop then returns
	//   early most of the time.
	//2. Skip a line that is shorter than the shortest pattern.
	//3. Join the patterns into one expression with regexp.QuoteMeta and "|",
	//   and compile it one time. RE2 then builds one automaton and scans the
	//   line one time, whatever the pattern count.
	//4. Use an Aho-Corasick automaton, for example
	//   github.com/petar-dambovaliev/aho-corasick. It scans the line one time
	//   and its cost does not grow with the pattern count at all.
	//Measure before any of this. The program writes to a terminal, and a
	//person cannot read faster than a few thousand lines each second, so the
	//terminal write is the more likely limit. Write a Benchmark function with
	//a real log file before changing anything here.
	for _, pattern := range patterns {
		if strings.Contains(line, pattern) {
			return true
		}
	}
	return false
}

// truncateMiddle cuts text so that it occupies no more than limit columns. The
// cut is in the middle, because the start of a log line usually holds the time
// and the level, and the end holds the message, while the middle carries the
// least meaning. A reset is added at the end, so that a text mode which the cut
// left open cannot reach the following lines.
func truncateMiddle(text string, limit int) string {
	if limit <= 0 {
		return ""
	}

	// Counting runes treats every character as one column. This is correct for
	// plain ASCII output and wrong in four known ways:
	//
	// 1. An escape sequence is counted as the characters it is made of, so a
	//    coloured line looks longer than it is and is cut earlier than needed.
	//    More text is lost than necessary, but the line cannot wrap, so the
	//    display stays correct.
	// 2. A cut can land inside an escape sequence. The terminal then receives a
	//    part of a sequence and may swallow the characters that follow, while
	//    it waits for an end that never arrives. The reset at the end of the
	//    result limits the damage, but it does not prevent this.
	// 3. A CJK character and most emoji occupy two columns but count as one, so
	//    a line with such text is cut too late and can still wrap.
	// 4. A combining mark occupies no column but counts as one, so a line with
	//    accents is cut earlier than needed.
	//
	// github.com/mattn/go-runewidth corrects 3 and 4. Correcting 1 and 2 needs
	// a small parser for escape sequences. Both can wait until the need is real.
	printableCharactersLen := utf8.RuneCountInString(text)
	if printableCharactersLen <= limit {
		return text
	}

	budget := limit - len(ellipsis)
	if budget < 2 {
		// There is no room for a head, an ellipsis and a tail together.
		return ellipsis + resetAllModes
	}

	// The head takes the extra column when the budget is an odd number, because
	// the start of a line is read first.
	headWidth := (budget + 1) / 2
	tailWidth := budget - headWidth

	// Cutting a slice of runes, and not the bytes of the string, keeps every
	// character whole.
	characters := []rune(text)
	head := string(characters[:headWidth])
	tail := string(characters[printableCharactersLen-tailWidth:])

	return head + ellipsis + tail + resetAllModes
}
