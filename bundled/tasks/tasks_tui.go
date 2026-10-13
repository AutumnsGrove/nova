//go:build !notui

package tasks

import "fmt"

// Card is the one-line tool-card summary the TUI shows for a todo call. It is
// in a build-tagged file so the notui build keeps only the tool logic.
func Card(tasks []Task) string {
	done := 0
	for _, t := range tasks {
		if t.Status == statusDone {
			done++
		}
	}
	return fmt.Sprintf("%d tasks, %d done", len(tasks), done)
}

// WidgetTag is the widget key the tasks extension publishes under.
func WidgetTag() string { return "tasks" }
