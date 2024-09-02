package handler

import (
	"log/slog"

	rotation "github.com/wytools/rlog/rotation"
)

// GetDefaultDailyLogger
func GetDefaultDailyLogger(filename string, h, m int) *slog.Logger {
	fileLog, err := rotation.NewDailyRotatedLogger("logs/out.log", h, m)
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
	fileLog, err := rotation.NewSizeRotatedLogger("logs/out.log", size, number)
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
