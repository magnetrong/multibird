// Package tui will hold the bubbletea/lipgloss single-screen live view of all
// instances (state, IP, peers, routes, DNS domains, recent log lines). v0.3 —
// see ROADMAP.md. Until then, `multibird status --json` is the aggregated,
// script-friendly view.
package tui

import "errors"

// ErrNotImplemented marks the v0.3 surface.
var ErrNotImplemented = errors.New("the TUI lands in v0.3 — use `multibird status` (or --json) meanwhile")

// Run starts the TUI. v0.3.
func Run() error { return ErrNotImplemented }
