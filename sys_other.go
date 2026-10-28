//go:build !windows

package main

import "os/exec"

func setupConsole() {}

func openBrowser(url string) { exec.Command("xdg-open", url).Start() }
