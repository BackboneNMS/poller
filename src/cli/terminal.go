package cli

import (
	"regexp"
	"strings"
)

// escapeSequences matches the terminal control sequences devices send for colors, cursor movement and window titles
var escapeSequences = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][A-Za-z0-9]|\x1b[@-Z\\-_]`)

// cleanTerminal turns raw terminal output into plain text: control sequences and NUL bytes are removed, backspaces
// erase the character before them, and a carriage return in the middle of a line (a pager prompt being overwritten, for
// example) keeps only what was written after it. Lines are separated by "\n" alone.
func cleanTerminal(raw string) string {
	text := escapeSequences.ReplaceAllString(raw, "")
	text = strings.ReplaceAll(text, "\x00", "")

	lines := strings.Split(text, "\n")

	for i, line := range lines {
		line = strings.TrimRight(line, "\r")

		if index := strings.LastIndexByte(line, '\r'); index >= 0 {
			line = line[index+1:]
		}

		lines[i] = applyBackspaces(line)
	}

	return strings.Join(lines, "\n")
}

func applyBackspaces(line string) string {
	if !strings.ContainsRune(line, '\b') {
		return line
	}

	result := make([]rune, 0, len(line))

	for _, char := range line {
		if char != '\b' {
			result = append(result, char)
		} else if len(result) > 0 {
			result = result[:len(result)-1]
		}
	}

	return string(result)
}

// lastLine returns the cleaned text after the last line break of raw output, which is where a device's prompt is
func lastLine(raw []byte) string {
	start := 0

	for i := len(raw) - 1; i >= 0; i-- {
		if raw[i] == '\n' {
			start = i + 1

			break
		}
	}

	return cleanTerminal(string(raw[start:]))
}

// echoSearchLines is how many lines at the start of a command's output are searched for its echo, which can follow a
// blank line or a repeated prompt. The search stops there so a config that mentions the command is not cut short.
const echoSearchLines = 3

// commandOutput extracts what a command printed from everything received between sending it and the next prompt:
// the echo of the command, the prompt, and blank lines at either end are removed. Every prompt at the end is removed,
// not only the last line, since some devices show the prompt twice (RouterOS takes the "\r\n" ending a command as
// two presses of Enter, for example).
func commandOutput(raw []byte, command string, prompt *regexp.Regexp) string {
	lines := strings.Split(cleanTerminal(string(raw)), "\n")

	for len(lines) > 0 && (len(strings.TrimSpace(lines[len(lines)-1])) < 1 || prompt.MatchString(lines[len(lines)-1])) {
		lines = lines[:len(lines)-1]
	}

	for i := 0; i < len(lines) && i < echoSearchLines; i++ {
		if strings.Contains(lines[i], command) {
			lines = lines[i+1:]

			break
		}
	}

	return trimBlankLines(strings.Join(lines, "\n"))
}

// trimBlankLines removes the empty lines at the start and end of text, and the trailing spaces of every line
func trimBlankLines(text string) string {
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}

	for len(lines) > 0 && len(lines[0]) < 1 {
		lines = lines[1:]
	}

	for len(lines) > 0 && len(lines[len(lines)-1]) < 1 {
		lines = lines[:len(lines)-1]
	}

	return strings.Join(lines, "\n")
}
