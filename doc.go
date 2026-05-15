// Package ygo is a pure-Go port of the Yjs CRDT framework.
//
// ygo is binary-protocol compatible with the npm yjs package (V1 update encoding),
// allowing JavaScript clients to synchronize seamlessly with Go servers and vice versa.
//
// Pure-Go means no CGO, so gomobile bind works for iOS/Android targets.
//
// Status: pre-alpha. Public API is unstable.
//
// See https://github.com/Deln0r/ygo for documentation and examples.
package ygo

// Version is the current ygo version.
const Version = "0.0.0-dev"
