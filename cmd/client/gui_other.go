//go:build !windows

package main

const guiAvailable = false

func runGUI(string) int { return 2 }
