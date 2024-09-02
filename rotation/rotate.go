// Package wangyi provides a list of unit tools.
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

type RotateType int

const (
	RotateDaily RotateType = 1
	RotateSize  RotateType = 2
)

// ensure we always implement io.WriteCloser
var _ io.WriteCloser = (*Logger)(nil)

// Logger is an io.WriteCloser that writes to the specified filename.
//
// Logger opens or creates the logfile on first Write.  If the file exists and
// is less than MaxSize megabytes, lumberjack will open and append to that file.
// If the file exists and its size is >= MaxSize megabytes, the file is renamed
// by putting the current time in a timestamp in the name immediately before the
// file's extension (or the end of the filename if there's no extension). A new
// log file is then created using original filename.
//
// Whenever a write would cause the current log file exceed MaxSize megabytes,
// the current file is closed, renamed, and a new log file created with the
// original name. Thus, the filename you give Logger is always the "current" log
// file.
//
// Backups use the log file name given to Logger, in the form
// `name-timestamp.ext` where name is the filename without the extension,
// timestamp is the time at which the log was rotated formatted with the
// time.Time format of `2006-01-02T15-04-05.000` and the extension is the
// original extension.  For example, if your Logger.Filename is
// `/var/log/foo/server.log`, a backup created at 6:30pm on Nov 11 2016 would
// use the filename `/var/log/foo/server-2016-11-04T18-30-00.000.log`
//
// # Cleaning Up Old Log Files
//
// Whenever a new logfile gets created, old log files may be deleted.  The most
// recent files according to the encoded timestamp will be retained, up to a
// number equal to MaxBackups (or all of them if MaxBackups is 0).  Any files
// with an encoded timestamp older than MaxAge days are deleted, regardless of
// MaxBackups.  Note that the time encoded in the timestamp is the rotation
// time, which may differ from the last time that file was written to.
//
// If MaxBackups and MaxAge are both 0, no old log files will be deleted.
type Logger struct {
	// Filename is the file to write logs to.  Backup log files will be retained in the same directory.
	filename string

	// RotateType is the type of log file name rotating. If it is RotateDaily, the log file will change everyday.
	// If it is RotateSize, the log file will change when the file has grown over the MaxSize.
	// Rhour, Rminute and Rsecond are set when RotateType = RotateDaily. The log file will change when time is matched everyday.
	// RMaxSize, RMaxNum are set when RotateType = RotateSize. Th log file will change when its size is over the RMaxSize.
	// When the numbers of files reaches RMaxNum, the first log file will be overwritten.
	rotateType      RotateType
	rHour           int
	rMinute         int
	currentFileTime time.Time
	rMaxSize        int64
	rSize           int64
	rMaxNum         int
	fnRotateIndex   int
	fnRotate        []string
	fnRotateUsed    []bool

	file *os.File

	sync.Mutex
}

func NewDailyRotatedLogger(filename string, h, m int) (*Logger, error) {
	l := &Logger{
		filename:   filename,
		rotateType: RotateDaily,
		rHour:      h,
		rMinute:    m,
	}
	var err error
	l.file, err = l.openNewDailyFile()
	return l, err
}

func NewSizeRotatedLogger(filename string, size int64, number int) (*Logger, error) {
	if size < 0 {
		size = 1024 * 1024
	}
	if number < 1 {
		number = 10
	}
	l := &Logger{
		filename:      filename,
		rotateType:    RotateSize,
		rMaxSize:      size,
		rMaxNum:       number,
		fnRotateIndex: -1,
		rSize:         size,
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

func (l *Logger) openNewDailyFile() (*os.File, error) {
	path, fn, suffix, err := getPathFileName(l.filename)
	if err != nil {
		return nil, err
	}

	l.currentFileTime = time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), l.rHour, l.rMinute, 0, 0, time.Local)
	if l.currentFileTime.After(time.Now()) {
		l.currentFileTime = l.currentFileTime.AddDate(0, 0, -1)
	}

	ts := time.Now().Format("_20060102_1504")

	return os.OpenFile(path+fn+ts+suffix, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0666)
}

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
	l.rotate()
	n, err = l.file.Write(p)
	l.rSize += int64(n)
	return n, err
}

func (l *Logger) rotate() {
	var logFile *os.File
	var err error
	bNeedRotate := false
	switch l.rotateType {
	case RotateDaily:
		if time.Now().AddDate(0, 0, -1).After(l.currentFileTime) {
			logFile, err = l.openNewDailyFile()
			bNeedRotate = true
		}
	case RotateSize:
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

// Close implements io.Closer, and closes the current logfile.
func (l *Logger) Close() error {
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

// getPathFileName return the filename's fullpath, filename and the suffix
func getPathFileName(fn string) (string, string, string, error) {
	var path, file, suffix string
	if len(fn) > 0 {
		indexFile := strings.LastIndex(fn, "/")
		indexLastDot := strings.LastIndex(fn, ".")

		if indexLastDot > 0 && indexFile < indexLastDot {
			file = fn[indexFile+1 : indexLastDot]
			if indexLastDot < (len(fn) - 1) {
				suffix = fn[indexLastDot:]
			}
		} else if indexLastDot == -1 {
			file = fn[indexFile+1:]
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
	return path, file, suffix, os.MkdirAll(path, os.ModePerm)
}
