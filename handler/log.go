package handler

import (
	"log/slog"

	"github.com/wytools/rlog/rotation"
)

// GetDefaultDailyLogger
func GetDefaultDailyLogger(filename string, h, m int) *slog.Logger {
	fileLog, err := rotation.NewDailyLogger(filename, h, m, false)
	if err != nil {
		panic(err)
	}

	opts := slog.HandlerOptions{
		AddSource:   true,
		Level:       slog.LevelDebug,
		ReplaceAttr: nil,
	}
	return slog.New(NewDefaultHandler(fileLog, &opts))
}

func GetDefaultSizeLogger(filename string, size int64, number int) *slog.Logger {
	fileLog, err := rotation.NewSizeLogger(filename, size, number, true)
	if err != nil {
		panic(err)
	}

	opts := slog.HandlerOptions{
		AddSource:   true,
		Level:       slog.LevelDebug,
		ReplaceAttr: nil,
	}
	return slog.New(NewDefaultHandler(fileLog, &opts))
}
