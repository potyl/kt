package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
)

var (
	colorRed   = color.New(color.FgRed, color.Bold).SprintFunc()
	colorGreen = color.New(color.FgGreen, color.Bold).SprintFunc()
	colorBlue  = color.New(color.FgBlue, color.Bold).SprintFunc()
	colorCyan  = color.New(color.FgCyan, color.Bold).SprintFunc()
)

func autoscalerColor(autoscaler string, width int) string {
	padded := fmt.Sprintf("%-*s", width, autoscaler)
	if autoscaler == "managed" {
		return colorRed(padded)
	}
	return padded
}

func archColor(arch string, width int) string {
	padded := fmt.Sprintf("%-*s", width, arch)
	switch arch {
	case "arm64":
		return colorGreen(padded)
	case "amd64":
		return colorCyan(padded)
	default:
		return padded
	}
}

func nodepoolColor(nodepool string, width int) string {
	for _, arch := range []string{"arm64", "amd64"} {
		before, after, found := strings.Cut(nodepool, arch)
		if !found {
			continue
		}
		var coloredArch string
		if arch == "arm64" {
			coloredArch = colorGreen(arch)
		} else {
			coloredArch = colorCyan(arch)
		}
		result := before + coloredArch + after
		if pad := width - len(nodepool); pad > 0 {
			result += strings.Repeat(" ", pad)
		}
		return result
	}
	return fmt.Sprintf("%-*s", width, nodepool)
}

func humanDuration(d time.Duration) string {
	if d < 0 {
		return "0s"
	}
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := int(d.Minutes())
	if m < 60 {
		if rem := s % 60; rem != 0 {
			return fmt.Sprintf("%dm%ds", m, rem)
		}
		return fmt.Sprintf("%dm", m)
	}
	h := int(d.Hours())
	if h < 24 {
		if rem := m % 60; rem != 0 {
			return fmt.Sprintf("%dh%dm", h, rem)
		}
		return fmt.Sprintf("%dh", h)
	}
	days := int(d.Hours() / 24)
	if days < 365 {
		if rem := h % 24; rem != 0 {
			return fmt.Sprintf("%dd%dh", days, rem)
		}
		return fmt.Sprintf("%dd", days)
	}
	years := days / 365
	if rem := days % 365; rem != 0 {
		return fmt.Sprintf("%dy%dd", years, rem)
	}
	return fmt.Sprintf("%dy", years)
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic("could not determine home directory")
	}
	return home
}
