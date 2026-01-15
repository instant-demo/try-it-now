package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNew_DefaultLevel(t *testing.T) {
	logger := New("", "text")
	if logger == nil {
		t.Fatal("New() returned nil")
	}
}

func TestNew_Levels(t *testing.T) {
	tests := []struct {
		level    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"invalid", slog.LevelInfo}, // defaults to info
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			logger := New(tt.level, "text")
			if logger == nil {
				t.Fatal("New() returned nil")
			}
		})
	}
}

func TestNew_Formats(t *testing.T) {
	// Both formats should work without error
	jsonLogger := New("info", "json")
	if jsonLogger == nil {
		t.Fatal("New(json) returned nil")
	}

	textLogger := New("info", "text")
	if textLogger == nil {
		t.Fatal("New(text) returned nil")
	}
}

func TestLogger_With(t *testing.T) {
	logger := New("info", "text")
	childLogger := logger.With("key", "value")

	if childLogger == nil {
		t.Fatal("With() returned nil")
	}

	// Verify it's a different instance
	if childLogger == logger {
		t.Error("With() should return a new logger instance")
	}
}

func TestNop(t *testing.T) {
	logger := Nop()
	if logger == nil {
		t.Fatal("Nop() returned nil")
	}

	// Should not panic when logging
	logger.Info("test message", "key", "value")
	logger.Warn("test warning")
	logger.Error("test error")
	logger.Debug("test debug")
}

func TestDefault(t *testing.T) {
	logger := Default()
	if logger == nil {
		t.Fatal("Default() returned nil")
	}
}

func TestNopWriter(t *testing.T) {
	w := nopWriter{}
	n, err := w.Write([]byte("test"))
	if err != nil {
		t.Errorf("nopWriter.Write() error = %v", err)
	}
	if n != 4 {
		t.Errorf("nopWriter.Write() = %d, want 4", n)
	}
}

func TestLogger_Output(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := &Logger{Logger: slog.New(handler)}

	logger.Info("test message", "key", "value")

	output := buf.String()
	if !strings.Contains(output, "test message") {
		t.Errorf("Log output missing message: %s", output)
	}
	if !strings.Contains(output, "key=value") {
		t.Errorf("Log output missing key=value: %s", output)
	}
}
