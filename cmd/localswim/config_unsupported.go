//go:build !windows || !amd64

package main

func readConfig(string) (runtimeConfig, error)               { return runtimeConfig{}, errComposition }
func pinRuntimeConfig(runtimeConfig, string) (func(), error) { return nil, errComposition }
