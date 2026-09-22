// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 mastmq

package listeners

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"syscall"
)

// logEstablishError reports the outcome of establishing a connection.
//
// A peer that opens a connection and closes it without sending a packet is
// not an incident. It is a TCP health check, a load balancer, or a port
// scanner, and every one of them used to produce a warning: a Kubernetes
// readinessProbe with tcpSocket on the MQTT port emits one every period, so
// a broker with the default ten-second probe logs 8,640 warnings a day and
// teaches whoever reads them that this logger is noise.
//
// A failure after the client has started speaking MQTT is still a warning,
// because then something really did go wrong mid-handshake.
func logEstablishError(log *slog.Logger, err error) {
	if err == nil {
		return
	}

	if quietDisconnect(err) {
		log.Debug("connection closed before a packet was read", "error", err)

		return
	}

	log.Warn("establishing connection", "error", err)
}

// quietDisconnect reports whether err is an ordinary peer hang-up rather
// than a fault worth telling an operator about.
func quietDisconnect(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE)
}
