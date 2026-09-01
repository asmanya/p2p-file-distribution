// Package tracker fetches peer addresses from a BitTorrent tracker over HTTP. It does not talk to peers
// directly — it only returns their addresses; that boundary matters for the layers above.
package tracker

// Personal notes (not part of the package doc, just for my own understanding):
//
// Flow: metainfo.Torrent (info hash, announce URL) -> request.go builds the
// announce URL -> client.go does the HTTP GET -> response.go decodes the
// bencoded reply into peer addresses -> tiers.go retries across multiple
// tracker URLs if the first one fails.
//
// The whole package only ever produces a []netip.AddrPort. It never opens a
// connection to a peer itself - that's the peer package's job (Phase 4). This
// package's only responsibility is "who do I talk to", not "how do I talk to them".
//
// File by file:
//   - peerid.go:   GeneratePeerID() - this client's 20-byte identity, once per session.
//   - request.go:  BuildAnnounceURL() - turns (announce URL, info hash, peer ID, port, left)
//                  into the full GET URL, percent-encoding the raw binary fields.
//   - response.go: ParseAnnounceResponse() - bencode dict -> AnnounceResponse struct,
//                  handling both compact (byte-string) and legacy (dict-list) peer formats.
//   - client.go:   Client.Announce() - the actual HTTP GET, with timeout, size cap, and
//                  status check so a bad tracker can't hang or crash this program.
//   - tiers.go:    Client.AnnounceAll() - walks announce + announce-list in order, skips
//                  non-HTTP schemes (e.g. udp), returns the first tracker that responds.
