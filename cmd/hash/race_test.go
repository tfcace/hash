//go:build race

package main

// raceBuild makes hashBinary build hash with the race detector when the
// tests run with -race, so the launch tests check hash's own goroutines.
const raceBuild = true
