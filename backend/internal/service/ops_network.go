package service

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type OpsNetworkPoint struct {
	At             time.Time `json:"at"`
	Instance       string    `json:"instance"`
	EgressMbps     *float64  `json:"egress_mbps"`
	RetransPercent *float64  `json:"tcp_retrans_percent"`
	QdiscDrops     *int64    `json:"qdisc_drops"`
	EgressStatus   string    `json:"egress_status"`
	TCPStatus      string    `json:"tcp_status"`
	QdiscStatus    string    `json:"qdisc_status"`
	WindowSeconds  float64   `json:"window_seconds"`
}
type OpsBodyBytesPoint struct {
	At      time.Time `json:"at"`
	Bytes   int64     `json:"bytes"`
	Samples int64     `json:"samples"`
}
type OpsNetworkSeries struct {
	Points     []OpsNetworkPoint   `json:"points"`
	BodyPoints []OpsBodyBytesPoint `json:"body_points"`
	Scope      string              `json:"scope"`
}
type opsNetworkStore interface {
	InsertNetworkSample(context.Context, OpsNetworkPoint) error
	ReadNetworkSeries(context.Context, time.Time, time.Time) (*OpsNetworkSeries, error)
}

type networkCounters struct {
	interfaces           map[string]uint64
	out, retrans         uint64
	netOK, tcpOK         bool
	netStatus, tcpStatus string
	at                   time.Time
}
type hostNetworkSampler struct {
	readQdisc     func() qdiscSnapshot
	previousQdisc *qdiscSnapshot
	readFile      func(string) ([]byte, error)
	previous      *networkCounters
}

func networkReadStatus(err error) string {
	if os.IsPermission(err) {
		return "permission_denied"
	}
	if os.IsNotExist(err) {
		return "unsupported"
	}
	return "read_error"
}

func parseNetworkCounters(read func(string) ([]byte, error), now time.Time) networkCounters {
	c := networkCounters{at: now, interfaces: map[string]uint64{}, netStatus: "malformed", tcpStatus: "malformed"}
	if data, err := read("/proc/net/dev"); err == nil {
		valid := true
		for _, line := range strings.Split(string(data), "\n") {
			name, raw, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			name = strings.TrimSpace(name)
			if name == "lo" {
				continue
			}
			fields := strings.Fields(raw)
			if len(fields) < 16 {
				valid = false
				break
			}
			v, err := strconv.ParseUint(fields[8], 10, 64)
			if err != nil {
				valid = false
				break
			}
			c.interfaces[name] = v
		}
		c.netOK = valid && len(c.interfaces) > 0
		if valid && len(c.interfaces) == 0 {
			c.netStatus = "no_interfaces"
		}
	} else {
		c.netStatus = networkReadStatus(err)
	}
	if data, err := read("/proc/net/snmp"); err == nil {
		lines := strings.Split(string(data), "\n")
		for i := 0; i+1 < len(lines); i++ {
			header, values := strings.Fields(lines[i]), strings.Fields(lines[i+1])
			if len(header) < 2 || len(values) != len(header) || header[0] != "Tcp:" || values[0] != "Tcp:" {
				continue
			}
			found := 0
			for n, key := range header {
				if key != "OutSegs" && key != "RetransSegs" {
					continue
				}
				v, err := strconv.ParseUint(values[n], 10, 64)
				if err != nil {
					break
				}
				if key == "OutSegs" {
					c.out = v
				} else {
					c.retrans = v
				}
				found++
			}
			c.tcpOK = found == 2
			break
		}
	} else {
		c.tcpStatus = networkReadStatus(err)
	}
	return c
}

func (s *hostNetworkSampler) sample(now time.Time, instance string) OpsNetworkPoint {
	read := s.readFile
	if read == nil {
		read = os.ReadFile
	}
	c := parseNetworkCounters(read, now)
	p := OpsNetworkPoint{At: now, Instance: instance, EgressStatus: c.netStatus, TCPStatus: c.tcpStatus, QdiscStatus: "not_collected"}
	var q qdiscSnapshot
	if s.readQdisc != nil {
		q = s.readQdisc()
	} else {
		q = qdiscSnapshot{status: "not_collected"}
	}
	previousQ := s.previousQdisc
	s.previousQdisc = &q
	p.QdiscStatus = q.status
	previous := s.previous
	s.previous = &c
	if c.netOK {
		p.EgressStatus = "initial_sample"
	}
	if c.tcpOK {
		p.TCPStatus = "initial_sample"
	}
	if previous == nil {
		if q.status == "available" {
			p.QdiscStatus = "initial_sample"
		}
		return p
	}
	seconds := now.Sub(previous.at).Seconds()
	p.WindowSeconds = seconds
	if seconds < 45 || seconds > 90 {
		if q.status == "available" {
			p.QdiscStatus = "uneven_window"
		}
		if c.netOK {
			p.EgressStatus = "uneven_window"
		}
		if c.tcpOK {
			p.TCPStatus = "uneven_window"
		}
		return p
	}
	if q.status == "available" && previousQ != nil && previousQ.status == "available" {
		valid := len(q.drops) == len(previousQ.drops)
		var delta int64
		for key, value := range q.drops {
			old, ok := previousQ.drops[key]
			if !ok || value < old {
				valid = false
				break
			}
			delta += int64(value - old)
		}
		p.QdiscStatus = "counter_reset_or_topology_change"
		if valid {
			p.QdiscDrops = &delta
			p.QdiscStatus = "measured_root_egress_qdisc"
		}
	} else if q.status == "available" {
		p.QdiscStatus = "initial_sample"
	}
	if c.netOK && previous.netOK {
		p.EgressStatus = "counter_reset_or_topology_change"
		valid := len(c.interfaces) == len(previous.interfaces)
		delta := float64(0)
		for name, value := range c.interfaces {
			old, ok := previous.interfaces[name]
			if !ok || value < old {
				valid = false
				break
			}
			delta += float64(value - old)
		}
		if valid {
			value := delta * 8 / seconds / 1e6
			p.EgressMbps = &value
			p.EgressStatus = "measured"
		}
	}
	if c.tcpOK && previous.tcpOK {
		p.TCPStatus = "counter_reset"
		if c.out >= previous.out && c.retrans >= previous.retrans {
			p.TCPStatus = "no_segments"
			if c.out > previous.out {
				value := float64(c.retrans-previous.retrans) * 100 / float64(c.out-previous.out)
				p.RetransPercent = &value
				p.TCPStatus = "measured"
			}
		}
	}
	return p
}

// Each instance stores only its own readable namespace counters, before the
// cluster-wide application-metrics leader lock. No probes or shell commands.
func (c *OpsMetricsCollector) collectNetwork(ctx context.Context, now time.Time) error {
	store, ok := c.opsRepo.(opsNetworkStore)
	if !ok {
		return nil
	}
	if c.networkSampler == nil {
		c.networkSampler = &hostNetworkSampler{readQdisc: readHostQdisc}
	}
	return store.InsertNetworkSample(ctx, c.networkSampler.sample(now, c.instanceID))
}

func (s *OpsService) NetworkSeries(ctx context.Context, start, end time.Time) (*OpsNetworkSeries, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, err
	}
	if !end.After(start) || end.Sub(start) > 24*time.Hour {
		return nil, infraerrors.BadRequest("NETWORK_RANGE_INVALID", "Network series range must be positive and at most 24 hours")
	}
	store, ok := s.opsRepo.(opsNetworkStore)
	if !ok {
		return nil, fmt.Errorf("network storage unavailable")
	}
	return store.ReadNetworkSeries(ctx, start, end)
}
