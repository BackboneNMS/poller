package main

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
)

var pingAverageRegExp = regexp.MustCompile(`= [\d.]+/([\d.]+)/`)

func PingHost(ctx context.Context, target string) (bool, *float64) {
	output, err := exec.CommandContext(ctx, "ping", "-q", "-n", "-c", "3", "-i", "0.2", "-W", "1", target).Output()

	if err != nil {
		return false, nil
	}

	match := pingAverageRegExp.FindSubmatch(output)

	if match == nil {
		return true, nil
	}

	latency, err := strconv.ParseFloat(string(match[1]), 64)

	if err != nil {
		return true, nil
	}

	return true, &latency
}
