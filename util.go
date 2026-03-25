package main

import "strconv"

func parseF(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func copyAttrs(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
