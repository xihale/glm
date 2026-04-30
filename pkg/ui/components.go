package ui

import (
	"fmt"
	"strings"
	"time"
)

// Table represents a simple CLI table
type Table struct {
	Headers []string
	Rows    [][]string
}

func NewTable(headers ...string) *Table {
	return &Table{Headers: headers}
}

func (t *Table) AddRow(row ...string) {
	t.Rows = append(t.Rows, row)
}

func (t *Table) Render() {
	if len(t.Headers) == 0 && len(t.Rows) == 0 {
		return
	}

	// Calculate column widths
	widths := make([]int, len(t.Headers))
	for i, h := range t.Headers {
		widths[i] = len(h)
	}
	for _, row := range t.Rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Render headers
	for i, h := range t.Headers {
		fmt.Printf("%s", Style(fmt.Sprintf("%-*s", widths[i]+2, h), Gray, Bold))
	}
	fmt.Println()

	// Render separator
	for _, w := range widths {
		fmt.Printf("%s  ", strings.Repeat("-", w))
	}
	fmt.Println()

	// Render rows
	for _, row := range t.Rows {
		for i, cell := range row {
			if i < len(widths) {
				fmt.Printf("%-*s", widths[i]+2, cell)
			}
		}
		fmt.Println()
	}
	fmt.Println()
}

// Spinner represents a simple CLI spinner
type Spinner struct {
	frames  []string
	stop    chan bool
	message string
}

func NewSpinner(msg string) *Spinner {
	return &Spinner{
		frames:  []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		stop:    make(chan bool),
		message: msg,
	}
}

func (s *Spinner) Start() {
	go func() {
		i := 0
		for {
			select {
			case <-s.stop:
				fmt.Print("\r\033[K") // Clear line
				return
			default:
				fmt.Printf("\r%s %s", Style(s.frames[i], Cyan, Bold), s.message)
				i = (i + 1) % len(s.frames)
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()
}

func (s *Spinner) Stop() {
	s.stop <- true
}
