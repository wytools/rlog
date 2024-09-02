package rotation

import "log/slog"

func GetDefaultDailyLogger(filename string, h, m int) *slog.Logger {
	fileLog, err := NewDailyRotatedLogger("logs/out.log", h, m)
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
	fileLog, err := NewSizeRotatedLogger("logs/out.log", size, number)
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
