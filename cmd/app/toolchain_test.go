package main

import (
	"go/version"
	"os"
	"regexp"
	"runtime"
	"testing"
)

// Araç zinciri satırı ile imaj aynı yamalı sürümü sabitler.
func TestToolchainPinned(t *testing.T) {
	gomod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^toolchain (go\d+\.\d+\.\d+)$`).FindSubmatch(gomod)
	if m == nil {
		t.Fatal("go.mod has no exact toolchain line")
	}
	want := string(m[1])

	dockerfile, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	d := regexp.MustCompile(`(?m)^FROM golang:(\S+)`).FindSubmatch(dockerfile)
	if d == nil {
		t.Fatal("Dockerfile has no golang base image")
	}
	if got := "go" + string(d[1]); got != want+"-alpine" {
		t.Errorf("Dockerfile builds with golang:%s; want golang:%s-alpine, the go.mod toolchain", d[1], want[2:])
	}

	if version.Compare(runtime.Version(), want) < 0 {
		t.Errorf("tests run on %s, older than the pinned %s", runtime.Version(), want)
	}
}
