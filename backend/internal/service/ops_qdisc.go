package service

import (
	"encoding/binary"
	"errors"
)

type qdiscKey struct{ ifindex, handle uint32 }
type qdiscSnapshot struct {
	drops  map[qdiscKey]uint64
	status string
}

// Linux UAPI tcmsg (20 bytes), TCA_STATS2/TCA_STATS_QUEUE, with legacy TCA_STATS
// fallback. Root egress only: nested parent/child totals must not be summed.
// https://docs.kernel.org/networking/gen_stats.html
func parseQdiscMessages(messages [][]byte) (qdiscSnapshot, error) {
	out := qdiscSnapshot{drops: map[qdiscKey]uint64{}, status: "no_root_statistics"}
	attrs := func(data []byte) (map[uint16][]byte, error) {
		m := map[uint16][]byte{}
		for len(data) >= 4 {
			n := int(binary.NativeEndian.Uint16(data[:2]))
			kind := binary.NativeEndian.Uint16(data[2:4]) & 0x3fff
			if n < 4 || n > len(data) {
				return nil, errors.New("invalid qdisc attribute")
			}
			m[kind] = data[4:n]
			step := (n + 3) &^ 3
			if step > len(data) {
				if n == len(data) {
					break
				}
				return nil, errors.New("truncated qdisc attribute")
			}
			data = data[step:]
		}
		return m, nil
	}
	for _, data := range messages {
		if len(data) < 20 {
			return out, errors.New("short tcmsg")
		}
		if binary.NativeEndian.Uint32(data[12:16]) != 0xffffffff {
			continue
		}
		m, err := attrs(data[20:])
		if err != nil {
			return out, err
		}
		var drops uint32
		found := false
		if nested, ok := m[7]; ok {
			stats, err := attrs(nested)
			if err != nil {
				return out, err
			}
			if queue, ok := stats[3]; ok {
				if len(queue) < 20 {
					return out, errors.New("short queue statistics")
				}
				drops = binary.NativeEndian.Uint32(queue[8:12])
				found = true
			}
		}
		if !found {
			if legacy, ok := m[3]; ok {
				if len(legacy) < 16 {
					return out, errors.New("short legacy qdisc statistics")
				}
				drops = binary.NativeEndian.Uint32(legacy[12:16])
				found = true
			}
		}
		if found {
			key := qdiscKey{binary.NativeEndian.Uint32(data[4:8]), binary.NativeEndian.Uint32(data[8:12])}
			if _, duplicate := out.drops[key]; duplicate {
				return out, errors.New("duplicate root qdisc")
			}
			out.drops[key] = uint64(drops)
		}
	}
	if len(out.drops) > 0 {
		out.status = "available"
	}
	return out, nil
}
