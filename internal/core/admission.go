package core

// Admitter decides whether a download may transfer right now.
//
// It exists because a queue that starts everything at once is not a queue. Ten
// grabs on a metered Usenet account, or twenty torrents over one connection,
// starve each other: every download crawls, none finishes, and the first
// import is as far away as the last. A cap turns that into a line — a few
// downloads running at full speed, the rest visibly waiting their turn.
//
// # Why the engines do not each own this
//
// The cap is two-level: one ceiling per download method (the embedded torrent
// engine, the embedded Usenet engine, each configured external client) and one
// ceiling across all of them together. No engine can enforce the second, because
// no engine can see the others. So the decision lives in one place above them
// all and the engines only ask.
//
// A nil Admitter means unlimited, which is what a Caravan with no caps
// configured has always done. Engines must treat it as "yes" without
// consulting anything, so the uncapped path stays byte-identical to the one
// that predates this interface.
//
// # The contract
//
// Request is the whole of the decision: an engine calls it before it starts
// transferring and honours the answer. A download that is refused is not an
// error and is not paused — it is queued, which is a state the queue already
// has and the user already understands.
//
// Release must be called for every granted slot, once the download is no
// longer using it: finished, failed, removed, paused, or (for a torrent) done
// downloading and now only seeding. Slots are about the downloading phase;
// nothing else consumes one.
//
// Implementations must not call back into an engine while holding their own
// lock. The engine calls Request under its own lock, so a coordinator that
// reached back the other way while locked would close the cycle.
type Admitter interface {
	// Request asks for a slot for one download. method is the engine's own
	// name — the `downloads.engine` value — and is what the per-method cap is
	// keyed on. It reports whether the download may transfer now.
	//
	// Request is idempotent per id: asking again for a download that already
	// holds a slot returns true and consumes nothing further.
	Request(method string, id DownloadID) bool
	// Release gives a slot back. It is a no-op for a download that does not
	// hold one, so a caller never has to track whether it was admitted.
	Release(id DownloadID)
}
