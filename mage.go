// mage.go
//go:build mage
// +build mage

package main

import (
	"fmt"
	"os"
	
	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

var Default = Build

func Build() error {
	fmt.Println("Building burnrate...")
	return sh.RunV("go", "build", "-trimpath", "-ldflags=-s -w", "-o", "burnrate", ".")
}

func Test() error {
	fmt.Println("Running tests...")
	return sh.RunV("go", "test", "-v", "./...")
}

func Lint() error {
	mg.Deps(InstallTools)
	fmt.Println("Linting...")
	return sh.RunV("staticcheck", "./...")
}

func InstallTools() error {
	fmt.Println("Installing staticcheck...")
	return sh.RunV("go", "install", "honnef.co/go/tools/cmd/staticcheck@latest")
}

func Run() error {
	mg.Deps(Build)
	fmt.Println("Running burnrate...")
	return sh.RunV("./burnrate")
}

func Clean() error {
	fmt.Println("Cleaning...")
	return os.RemoveAll("burnrate")
}
