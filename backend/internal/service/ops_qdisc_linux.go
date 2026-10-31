//go:build linux

package service

import (
	"encoding/binary"
	"syscall"
	"time"
)

// Read-only NETLINK_ROUTE dump, no elevated capability request and no shell.
// The kernel may deny it: preserve capability/permission status without probes.
func readHostQdisc() qdiscSnapshot {
	failed := func(err error) qdiscSnapshot {
		if err == syscall.EAGAIN || err == syscall.ETIMEDOUT {
			return qdiscSnapshot{status: "timeout"}
		}
		if err == syscall.EAFNOSUPPORT || err == syscall.EPROTONOSUPPORT || err == syscall.ENOPROTOOPT || err == syscall.EOPNOTSUPP {
			return qdiscSnapshot{status: "unsupported"}
		}
		return qdiscSnapshot{status: networkReadStatus(err)}
	}
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_ROUTE)
	if err != nil {
		return failed(err)
	}
	defer func() { _ = syscall.Close(fd) }()
	if err = syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &syscall.Timeval{Usec: 500000}); err != nil {
		return failed(err)
	}
	if err = syscall.Bind(fd, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return failed(err)
	}
	request := make([]byte, 36)
	binary.NativeEndian.PutUint32(request[:4], 36)
	binary.NativeEndian.PutUint16(request[4:6], 38)    // RTM_GETQDISC
	binary.NativeEndian.PutUint16(request[6:8], 0x301) // REQUEST | DUMP
	binary.NativeEndian.PutUint32(request[8:12], 1)
	if err = syscall.Sendto(fd, request, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return failed(err)
	}
	var payloads [][]byte
	total := 0
	deadline := time.Now().Add(500 * time.Millisecond)
	for range 64 {
		if time.Now().After(deadline) {
			return qdiscSnapshot{status: "timeout"}
		}
		remaining := syscall.NsecToTimeval(time.Until(deadline).Nanoseconds())
		if err = syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &remaining); err != nil {
			return failed(err)
		}
		buffer := make([]byte, 64<<10)
		n, _, flags, from, err := syscall.Recvmsg(fd, buffer, nil, 0)
		if err != nil {
			return failed(err)
		}
		peer, ok := from.(*syscall.SockaddrNetlink)
		if !ok || peer.Pid != 0 || flags&syscall.MSG_TRUNC != 0 {
			return qdiscSnapshot{status: "truncated_or_invalid_dump"}
		}
		total += n
		if total > 1<<20 {
			return qdiscSnapshot{status: "collection_limit"}
		}
		messages, err := syscall.ParseNetlinkMessage(buffer[:n])
		if err != nil {
			return qdiscSnapshot{status: "malformed"}
		}
		for _, m := range messages {
			if m.Header.Seq != 1 || m.Header.Flags&0x10 != 0 {
				return qdiscSnapshot{status: "interrupted_dump"}
			}
			switch m.Header.Type {
			case syscall.NLMSG_ERROR:
				if len(m.Data) < 4 {
					return qdiscSnapshot{status: "malformed"}
				}
				code := int32(binary.NativeEndian.Uint32(m.Data[:4]))
				if code != 0 {
					return failed(syscall.Errno(-code))
				}
			case syscall.NLMSG_DONE:
				if len(m.Data) >= 4 && int32(binary.NativeEndian.Uint32(m.Data[:4])) != 0 {
					return qdiscSnapshot{status: "interrupted_dump"}
				}
				out, err := parseQdiscMessages(payloads)
				if err != nil {
					return qdiscSnapshot{status: "malformed"}
				}
				return out
			case 36:
				payloads = append(payloads, append([]byte(nil), m.Data...))
			}
		}
	}
	return qdiscSnapshot{status: "collection_limit"}
}
