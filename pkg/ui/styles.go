package ui

import (
	"fmt"
	"strings"
)

const (
	Reset     = "\033[0m"
	Bold      = "\033[1m"
	Dim       = "\033[2m"
	Italic    = "\033[3m"
	Underline = "\033[4m"

	Black   = "\033[30m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	White   = "\033[37m"
	Gray    = "\033[90m"

	BgBlack   = "\033[40m"
	BgRed     = "\033[41m"
	BgGreen   = "\033[42m"
	BgYellow  = "\033[43m"
	BgBlue    = "\033[44m"
	BgMagenta = "\033[45m"
	BgCyan    = "\033[46m"
	BgWhite   = "\033[47m"
)

// Icons
const (
	IconSuccess = "✔"
	IconError   = "✘"
	IconInfo    = "ℹ"
	IconWarn    = "⚠"
	IconBullet  = "•"
	IconArrow   = "➜"
)

// Styling functions
func Style(s string, codes ...string) string {
	return strings.Join(codes, "") + s + Reset
}

func Success(msg string) {
	fmt.Printf("%s %s\n", Style(IconSuccess, Green, Bold), msg)
}

func Error(msg string) {
	fmt.Printf("%s %s\n", Style(IconError, Red, Bold), msg)
}

func Warn(msg string) {
	fmt.Printf("%s %s\n", Style(IconWarn, Yellow, Bold), msg)
}

func Info(msg string) {
	fmt.Printf("%s %s\n", Style(IconInfo, Blue, Bold), msg)
}

func Header(msg string) {
	fmt.Printf("\n%s\n%s\n", Style(msg, Cyan, Bold, Underline), strings.Repeat(" ", len(msg)))
}

func Dimmed(msg string) string {
	return Style(msg, Gray)
}

func Accent(msg string) string {
	return Style(msg, Cyan, Bold)
}
