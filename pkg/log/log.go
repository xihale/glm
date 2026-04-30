package log

import (
	"fmt"
	"os"
	"time"

	"github.com/xihale/glm/pkg/ui"
)

var DebugMode bool

func Infof(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(os.Stderr, "%s %s %s\n", 
		ui.Style(time.Now().Format("15:04:05"), ui.Gray),
		ui.Style("INFO", ui.Blue, ui.Bold),
		msg)
}

func Errorf(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(os.Stderr, "%s %s %s\n", 
		ui.Style(time.Now().Format("15:04:05"), ui.Gray),
		ui.Style("ERROR", ui.Red, ui.Bold),
		msg)
}

func Debugf(format string, a ...interface{}) {
	if !DebugMode {
		return
	}
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(os.Stderr, "%s %s %s\n", 
		ui.Style(time.Now().Format("15:04:05"), ui.Gray),
		ui.Style("DEBUG", ui.Magenta, ui.Bold),
		msg)
}

func Fatalf(format string, a ...interface{}) {
	Errorf(format, a...)
	os.Exit(1)
}
