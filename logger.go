package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	logFile     *os.File
	logFilePath string
	logMutex    sync.Mutex
	debugMode   bool
)

// InitLogger initializes the log file. It reads registry value 'LogFilePath' and
// falls back to executable directory SyslogAgent.log if empty.
func InitLogger() {
	// try registry
	if p, err := ReadRegistryString("LogFilePath"); err == nil && p != "" {
		logFilePath = p
	} else {
		// default to executable directory
		if exe, err := os.Executable(); err == nil {
			dir := filepath.Dir(exe)
			logFilePath = filepath.Join(dir, "SyslogAgent.log")
		} else {
			logFilePath = "SyslogAgent.log"
		}
	}

	// open file for append
	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		// if fail, fallback to stdout (no file)
		fmt.Fprintf(os.Stderr, "failed to open log file %s: %v\n", logFilePath, err)
		return
	}
	logFile = f
	LogInfo("Logger initialized, path=%s", logFilePath)
}

func closeLogger() {
	logMutex.Lock()
	defer logMutex.Unlock()
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}
}

func logWrite(level, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	ts := time.Now().Format("2006-01-02 15:04:05")
	line := fmt.Sprintf("%s [%s] %s\r\n", ts, level, text)

	logMutex.Lock()
	defer logMutex.Unlock()
	if logFile != nil {
		_, _ = logFile.WriteString(line)
	} else {
		// fallback to stderr
		fmt.Fprint(os.Stderr, line)
	}
}

func LogInfo(format string, args ...any)  { logWrite("INFO", format, args...) }
func LogWarn(format string, args ...any)  { logWrite("WARN", format, args...) }
func LogError(format string, args ...any) { logWrite("ERROR", format, args...) }
func Debug(format string, args ...any) {
	if debugMode {
		fmt.Printf(format+"\r\n", args...)
	}
}
