// Package artifact defines provider-independent materialization inputs.
//
// The package is deliberately below launch, runtime, provider and session
// code. It models bytes, directories and immutable content references that a
// caller has already authorized the preparer to read. Source authorization is
// separate from the access granted to a spawned child process.
//
// Paths are slash-separated, relative and byte-for-byte case-sensitive. The
// package does not normalize Unicode or fold case. A duplicate exact path is a
// collision. A file path that is also a parent of another entry is a prefix
// collision.
package artifact
