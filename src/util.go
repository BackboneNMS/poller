package main

import "fmt"

func RunRecovered(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("Recovered from panic", "name", name, "panic", fmt.Sprint(r))
		}
	}()

	fn()
}

func PointerOf[T any](v T) *T {
	return &v
}

func TruncateString(value string, length int) string {
	if len(value) <= length {
		return value
	}

	return value[:length]
}
