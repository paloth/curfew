/*
Copyright © 2026 NAME HERE <EMAIL ADDRESS>
*/
package main

import (
	// Embedded so curfew/timezone works whatever the base image ships.
	_ "time/tzdata"

	"curfew/cmd"
)

func main() {
	cmd.Execute()
}
