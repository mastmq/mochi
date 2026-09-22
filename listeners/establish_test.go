// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 mastmq

package listeners

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
)

// levelRecorder captures the level each record was logged at.
type levelRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (r *levelRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *levelRecorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.records = append(r.records, rec.Clone())

	return nil
}

func (r *levelRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *levelRecorder) WithGroup(string) slog.Handler      { return r }

func (r *levelRecorder) only() slog.Record {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.records) != 1 {
		panic(fmt.Sprintf("expected exactly one record, got %d", len(r.records)))
	}

	return r.records[0]
}

// TestLogEstablishError pins the levels, because the whole point of the
// split is that a warning from this logger should be worth reading.
func TestLogEstablishError(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		level slog.Level
	}{
		// What a tcpSocket readiness probe produces, every period,
		// forever.
		{"probe hangs up", io.EOF, slog.LevelDebug},
		{"wrapped hang-up", fmt.Errorf("read connection: %w", io.EOF), slog.LevelDebug},
		{"truncated packet", io.ErrUnexpectedEOF, slog.LevelDebug},
		// Something actually went wrong mid-handshake.
		{"real failure", errors.New("malformed connect packet"), slog.LevelWarn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &levelRecorder{mu: sync.Mutex{}, records: nil}

			logEstablishError(slog.New(rec), tc.err)

			if got := rec.only().Level; got != tc.level {
				t.Errorf("logged at %v, want %v", got, tc.level)
			}
		})
	}
}

// TestLogEstablishErrorIgnoresSuccess guards the common path: a connection
// that established fine must not log at all.
func TestLogEstablishErrorIgnoresSuccess(t *testing.T) {
	rec := &levelRecorder{mu: sync.Mutex{}, records: nil}

	logEstablishError(slog.New(rec), nil)

	rec.mu.Lock()
	defer rec.mu.Unlock()

	if len(rec.records) != 0 {
		t.Errorf("logged %d records for a successful connection", len(rec.records))
	}
}

// TestLogEstablishErrorMessageIsNotEmpty exists because the line it
// replaces was l.log.Warn("", "error", err) -- a log record with no message
// at all, which is what reached production as {"msg":"", ...}.
func TestLogEstablishErrorMessageIsNotEmpty(t *testing.T) {
	rec := &levelRecorder{mu: sync.Mutex{}, records: nil}

	logEstablishError(slog.New(rec), errors.New("boom"))

	if rec.only().Message == "" {
		t.Error("log record has no message")
	}
}
