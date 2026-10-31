//go:build !linux

package service

func readHostQdisc() qdiscSnapshot { return qdiscSnapshot{status: "unsupported"} }
