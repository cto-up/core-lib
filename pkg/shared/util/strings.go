package util

import (
	"os"
)

func Interpolate(template string, variables map[string]string) string {
	f := func(ph string) string {
		return variables[ph]
	}
	return os.Expand(template, f)
}
