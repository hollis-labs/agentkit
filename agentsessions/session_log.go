package agentsessions

import "os"

// openSessionLog opens a session's log (StartOptions.LogPath, or
// <WorkspaceDir>/logs/session.log) for appending, creating it if needed.
//
// It never truncates and always appends. A host may keep its own O_APPEND
// writer on the same file: Torque tees its first-turn error capture, redacted
// stderr and permission-decision lines into logs/session.log. os.Create
// truncated the file at Start and then wrote from its own offset, overwriting
// whatever the host had appended. With O_APPEND every write, the child's
// stderr included, lands at the current end of the file, after the host's.
//
// A fresh session does not truncate either: agentkit cannot know whether the
// host has already written to the file, and truncating would lose exactly
// those lines. A host that wants a fresh log per session passes a fresh path
// (or truncates it itself before Start). A resumed or supervised-restarted
// session keeps appending to the same file. The mode is os.Create's.
func openSessionLog(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666) //nolint:gosec // G302/G304: workspace-managed path, os.Create's mode (umask applies)
}
