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
//   - choke.go:          recalcChoke() (tit-for-tat, upload-rate sorted once seeding) + rotateOptimistic(), setChoked().
//   - rates.go:          RateTracker - per-peer rolling download/upload rates, the choking algorithm's only input.
//   - serve.go:          serveRequest() - validates an incoming block request, reads it from disk, sends it back.
//   - worker.go:         worker()/serveIncoming() -> runConnection() - one loop for connections in either direction;
//                        session + handleMessage() - the single place inbound messages are handled; readLoop().
//   - piece.go:          Piece()/downloadBlocks() - single-piece download, per-block accounting, delegates every
//                        non-block message back to the session's handler.
//   - progress.go:       Progress - atomic counters (bytes up/down, peers, hash failures, duplicate assignments) + rate window.
//   - download.go:       Download() - wires coordinator + workers + listener + tracker + storage together, owns disk
//                        writes, and keeps running as a seeder once the last piece lands. Reports real uploaded/
//                        downloaded/left to the tracker on every announce, with started/completed/stopped events.
//   - queue.go:          Result type + resultsBufferSize - what the coordinator hands Download() for disk writes.
