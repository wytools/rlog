// Package rotaion provides a rotaional file logger.
//
// You can use it by importing "github.com/wytools/rlog/rotation"
//
// This package is a lightweight loggering file package that implements the io.Writer and
// io.Closer interfaces. It can be embedded into most logging packages such as zap, zerolog,
// including the Go's standard pacage "log/slog".
//
// It supports two types of log rotation. The first type is based on the date, swithing to a
// new file every day at the set time. The second type is based on file size and a set number
// of files. When each file exceeds the set size rMaxSize, it switches to a new one. When the
// number of files reaches the set total rMaxNum, it overwrites the oldest file.
//
// This package can set a locker.
package rotation

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RotationType is the type of log file name rotating. If it is DailyRotation, the log file will change everyday at a set time.
// If it is SizedRotation, the log file will change when the size of file has grown over the MaxSize.
type RotationType int

const (
	DailyRotation RotationType = 1 // rotated everyday at the set time
	SizedRotation RotationType = 2 // rotated when file exceeds the setting size
)

// ensure implement io.Write and io.Closer
var _ io.WriteCloser = (*Logger)(nil)

// Logger is a file logger which implement the io.WriteCloser interface.
type Logger struct {
	// filename is the file to write logs to. Daily logger files will have the same prefix and suffix but different datetime
	// format string file names. Size logger files will alse have the same prefix and suffix but different indexes number
	// format file names. All the files are retained in the same directory.
	filename string

	rType RotationType // DailyRotation or SizedRotation

	rHour           int       // the hour of the set time of DailyRotation logger
	rMinute         int       // the minute of the set time of RotatedDaily logger
	currentFileTime time.Time // the opening or creating time of the current log file.
	timeFormat      string    // the timeformat for the file name

	rMaxSize      int64    // the max size of per file, it represents the number of bytes. 1024 * 1024 * 1 = 1Mbytes
	rSize         int64    // the bytes size of current log file
	rMaxNum       int      // the max number of the file rotations
	fnRotateIndex int      // the index of current log file, it can be 0, 1, 2 ... rMaxNum-1
	fnRotate      []string // the file name of every log file for SizedRotation type, using fnRotateIndex can get a file name
	fnRotateUsed  []bool   // the index of file name has been used or not

	file *os.File // the current Writer

	bLock      bool // write with a lock or not
	sync.Mutex      // mutex lock for writing bytes
}

// Create a daily roation file logger, rotating at the set hour and minute
func NewDailyLogger(filename string, rHour, rMinute int, bLock bool) (*Logger, error) {
	l := &Logger{
		filename:   filename,
		rType:      DailyRotation,
		rHour:      rHour,
		rMinute:    rMinute,
		timeFormat: "_2006_01_02_15_04",
		bLock:      bLock,
	}
	var err error
	l.file, err = l.openNewDailyFile()
	return l, err
}

// Create a daily roation file logger, rotating at the set hour and minute, without lock
func NewDailyNoLockLogger(filename string, rHour, rMinute int) (*Logger, error) {
	return NewDailyLogger(filename, rHour, rMinute, false)
}

// Create a daily roation file logger, rotating at the set hour and minute, with a mutex lock
func NewDailyWithLockLogger(filename string, rHour, rMinute int) (*Logger, error) {
	return NewDailyLogger(filename, rHour, rMinute, true)
}

// Create a size rotation file logger, rotating when file size exceeds rMaxSize bytes.
// The maximum number of file rotations refers to the set limit on how many log files can be created
// and stored in a rotation cycle before the oldest file is overwritten to make room for new files.
func NewSizeLogger(filename string, rMaxSize int64, rMaxNum int, bLock bool) (*Logger, error) {
	if rMaxSize <= 0 {
		rMaxSize = 1024 * 1024
	}
	if rMaxNum < 1 {
		rMaxNum = 10
	}
	l := &Logger{
		filename:      filename,
		rType:         SizedRotation,
		rMaxSize:      rMaxSize,
		rMaxNum:       rMaxNum,
		fnRotateIndex: -1,
		rSize:         rMaxSize,
		bLock:         bLock,
	}
	path, fn, suffix, err := getPathFileName(filename)
	if err != nil {
		return nil, err
	}

	l.fnRotate = make([]string, l.rMaxNum)
	l.fnRotateUsed = make([]bool, l.rMaxNum)
	for i := 0; i < l.rMaxNum; i++ {
		l.fnRotate[i] = path + fn + strconv.Itoa(i) + suffix
		l.fnRotateUsed[i] = false
	}

	l.file, err = l.openNewSizeFile()
	return l, err
}

// Create a size rotation file logger, rotating when file size exceeds rMaxSize bytes.
// The maximum number of file rotations refers to the set limit on how many log files can be created
// and stored in a rotation cycle before the oldest file is overwritten to make room for new files.
// without lock
func NewSizeNoLockLogger(filename string, rMaxSize int64, rMaxNum int) (*Logger, error) {
	return NewSizeLogger(filename, rMaxSize, rMaxNum, false)
}

// Create a size rotation file logger, rotating when file size exceeds rMaxSize bytes.
// The maximum number of file rotations refers to the set limit on how many log files can be created
// and stored in a rotation cycle before the oldest file is overwritten to make room for new files.
// with a mutex lock
func NewSizeWithLockLogger(filename string, rMaxSize int64, rMaxNum int) (*Logger, error) {
	return NewSizeLogger(filename, rMaxSize, rMaxNum, true)
}

// Set the time format for file name, it can be used when RotationType = DailyRotate
func (l *Logger) SetTimeFormat(format string) {
	l.timeFormat = format
}

// open a new daily file
func (l *Logger) openNewDailyFile() (*os.File, error) {
	path, fn, suffix, err := getPathFileName(l.filename)
	if err != nil {
		return nil, err
	}

	l.currentFileTime = time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), l.rHour, l.rMinute, 0, 0, time.Local)
	if l.currentFileTime.After(time.Now()) {
		l.currentFileTime = l.currentFileTime.AddDate(0, 0, -1)
	}

	ts := time.Now().Format(l.timeFormat)

	return os.OpenFile(path+fn+ts+suffix, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0666)
}

// open a new size limit file
func (l *Logger) openNewSizeFile() (*os.File, error) {
	var logFile *os.File
	var err error
	for l.rSize >= l.rMaxSize {
		// rotate to get new filename
		l.fnRotateIndex++
		l.fnRotateIndex %= l.rMaxNum
		filename := l.fnRotate[l.fnRotateIndex]

		// if the new filename is used, the old file needs to be removed.
		if l.fnRotateUsed[l.fnRotateIndex] {
			if err = os.Remove(filename); err != nil {
				return nil, err
			}
		}

		logFile, err = os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			return nil, err
		}
		fInfo, err := logFile.Stat()
		if err != nil {
			return nil, err
		}
		l.rSize = fInfo.Size()
		l.fnRotateUsed[l.fnRotateIndex] = true
	}

	return logFile, nil
}

// Write implements io.Writer.
func (l *Logger) Write(p []byte) (n int, err error) {
	if l.bLock {
		l.Lock()
		defer l.Unlock()
	}
	l.rotate()
	n, err = l.file.Write(p)
	l.rSize += int64(n)
	return n, err
}

// the file will be rotated if the rotation condition is met, do it before writing bytes.
func (l *Logger) rotate() {
	var logFile *os.File = nil
	var err error
	bNeedRotate := false
	switch l.rType {
	case DailyRotation:
		if time.Now().AddDate(0, 0, -1).After(l.currentFileTime) {
			logFile, err = l.openNewDailyFile()
			bNeedRotate = true
		}
	case SizedRotation:
		if l.rSize >= l.rMaxSize {
			logFile, err = l.openNewSizeFile()
			bNeedRotate = true
		}
	}
	if bNeedRotate {
		l.file.Close()
		if err != nil {
			l.file = os.Stdout
		} else {
			l.file = logFile
		}
	}
}

// Close implements io.Closer, and closes the current file.
func (l *Logger) Close() error {
	l.Lock()
	defer l.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// Rotate causes Logger to close the existing log file and immediately create a
// new one.  This is a helper function for applications that want to initiate
// rotations outside of the normal rotation rules, such as in response to
// SIGHUP.  After rotating, this initiates compression and removal of old log
// files according to the configuration.

// getPathFileName return the filename's fullpath, prefix filename and the suffix
func getPathFileName(fn string) (string, string, string, error) {
	var path, prefix, suffix string
	if len(fn) > 0 {
		indexFile := strings.LastIndex(fn, "/")
		indexLastDot := strings.LastIndex(fn, ".")

		if indexLastDot > 0 && indexFile < indexLastDot {
			prefix = fn[indexFile+1 : indexLastDot]
			if indexLastDot < (len(fn) - 1) {
				suffix = fn[indexLastDot:]
			}
		} else if indexLastDot == -1 {
			prefix = fn[indexFile+1:]
		}
		if len(prefix) == 0 {
			prefix = "out"
		}
		if len(suffix) == 0 {
			suffix = ".log"
		}
		path = fn[0:(indexFile + 1)]
		var dir string
		var err error
		if (len(path) > 0 && path[0] != '/') || (len(path) == 0) {
			if dir, err = filepath.Abs(filepath.Dir(os.Args[0])); err != nil {
				return "", "", "", err
			}

			dir += "/"
		}
		path = dir + path
	}
	return path, prefix, suffix, os.MkdirAll(path, os.ModePerm)
}
