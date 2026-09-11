// Package download orchestrates a torrent download. It is the only package
// with full visibility into the rest of the system.
package download

// Personal notes (not part of the package doc, just for my own understanding):
//
// File by file:
//   - events.go:        Event/Command sealed interfaces - the vocabulary workers and the coordinator use to talk.
//   - coordinator.go:    Coordinator struct + Run() - single-owner state machine, zero mutexes.
//   - availability.go:   incAvailability/decAvailability/addAvailability/removeAvailability - swarm rarity tracking.
//   - selection.go:      selectPieceFor() (rarest-first), sendAssignCommand/assignPiece, freeStaleAssignments (per-assignee timeout).
//   - endgame.go:        remainingPieceCount/maybeEnterEndgame, assignAnyMissingTo (spreads across pieces), cancelOtherAssignees.
//   - worker.go:         worker() - event-driven per-peer loop, readLoop() - dedicated connection reader goroutine.
//   - piece.go:          EnsureUnchoked(), Piece()/downloadBlocks() - single-piece download over a messages/commands channel pair.
//   - progress.go:       Progress - atomic counters (bytes, peers, hash failures, duplicate assignments) + rate window.
//   - download.go:       Download() - wires coordinator + workers + tracker + storage together, owns disk writes.
//   - queue.go:          Result type + resultsBufferSize - what the coordinator hands Download() for disk writes.
