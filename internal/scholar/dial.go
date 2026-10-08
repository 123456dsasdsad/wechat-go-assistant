package scholar

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"time"
)

func publicIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}

// Resolve once, reject private answers, and pin the connection to a checked IP.
// SOCKS connects to that IP while net/http still validates the original TLS hostname.
func publicDial(proxy string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		addrs, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil || len(addrs) == 0 {
			return nil, errors.New("source_dns_failed")
		}
		for _, a := range addrs {
			if !publicIP(a.IP) {
				return nil, errors.New("private_source_address")
			}
		}
		dst := net.JoinHostPort(addrs[0].IP.String(), port)
		dialer := net.Dialer{Timeout: 15 * time.Second}
		if proxy == "" {
			return dialer.DialContext(ctx, network, dst)
		}
		conn, e := dialer.DialContext(ctx, "tcp", proxy)
		if e != nil {
			return nil, e
		}
		ok := false
		defer func() {
			if !ok {
				conn.Close()
			}
		}()
		deadline := time.Now().Add(30 * time.Second)
		if d, yes := ctx.Deadline(); yes && d.Before(deadline) {
			deadline = d
		}
		conn.SetDeadline(deadline)
		if _, e = conn.Write([]byte{5, 1, 0}); e != nil {
			return nil, e
		}
		reply := make([]byte, 2)
		if _, e = io.ReadFull(conn, reply); e != nil || reply[0] != 5 || reply[1] != 0 {
			return nil, errors.New("proxy_auth_failed")
		}
		request := []byte{5, 1, 0}
		ip := addrs[0].IP
		if v := ip.To4(); v != nil {
			request = append(request, 1)
			request = append(request, v...)
		} else {
			request = append(request, 4)
			request = append(request, ip.To16()...)
		}
		n, e := strconv.Atoi(port)
		if e != nil {
			return nil, e
		}
		request = binary.BigEndian.AppendUint16(request, uint16(n))
		if _, e = conn.Write(request); e != nil {
			return nil, e
		}
		reply = make([]byte, 4)
		if _, e = io.ReadFull(conn, reply); e != nil || reply[0] != 5 || reply[1] != 0 {
			return nil, errors.New("proxy_connect_failed")
		}
		size := 0
		switch reply[3] {
		case 1:
			size = 4
		case 4:
			size = 16
		case 3:
			one := make([]byte, 1)
			if _, e = io.ReadFull(conn, one); e != nil {
				return nil, e
			}
			size = int(one[0])
		default:
			return nil, errors.New("proxy_invalid_reply")
		}
		if _, e = io.CopyN(io.Discard, conn, int64(size+2)); e != nil {
			return nil, e
		}
		conn.SetDeadline(time.Time{})
		ok = true
		return conn, nil
	}
}
