package tools

import (
	"fmt"
	"runtime"
	"strconv"
)

func testPrintCommand(text string) string {
	quoted := strconv.Quote(text)
	if runtime.GOOS == "windows" {
		return "[Console]::Write(" + quoted + ")"
	}
	return "printf %s " + quoted
}

func testSleepCommand(seconds int) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("Start-Sleep -Seconds %d", seconds)
	}
	return fmt.Sprintf("sleep %d", seconds)
}

func testSleepThenPrintCommand(seconds int, text string) string {
	return testSleepCommand(seconds) + "; " + testPrintCommand(text)
}
