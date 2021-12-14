package main

import (
	"fmt"
)

func mustColorizeText(s, color string) string {
	result, err := colorizeText(s, color)
	if err != nil {
		panic(err)
	}
	return result
}

var ansiColorMap = map[string]string{
	"dark-red": "31m",
	"red":      "31;1m",

	"dark-green": "32m",
	"green":      "32;1m",

	"dark-blue": "34m",
	"blue":      "34;1m",

	"dark-magenta": "35m",
	"magenta":      "35;1m",
}

func colorizeText(s, color string) (string, error) {
	switch color {
	case "", "white":
		return s, nil
	default:
		escape, ok := ansiColorMap[color]
		if !ok {
			return "", fmt.Errorf("unsupported color: %s", color)
		}
		return "\033[" + escape + s + "\033[0m", nil
	}
}
