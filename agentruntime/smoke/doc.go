// Package smoke contains regression fixtures for recurring runtime bugs.
//
// The first implementation keeps executable tests in the packages that own the
// behavior: turn, sessionkit, runtimebind, and bootdir. This package is the
// import anchor for future cross-package smoke fixtures and live provider
// matrix tests.
package smoke
