//go:build !windows

package main

func prepareDesktopData([]string) (bool, error) { return false, nil }
