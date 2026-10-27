package relay

import (
	"net"
	"net/netip"
)

// A LAN relay (DESIGN_MULTIPLAYER §16.10) is a plaintext WebSocket relay a
// player runs on a computer at home for players nearby. Plaintext is safe
// only where nobody between the players can read or alter it, so the relay
// admits only peers whose address is the host's own or on a private network,
// and a client dials one only at such an address.

// lanAddr reports a loopback, private (RFC 1918, IPv6 unique local) or
// link-local address.
func lanAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast()
}

// lanPeer reports a connection from such an address.
func lanPeer(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	a, ok := netip.AddrFromSlice(tcp.IP)
	return ok && lanAddr(a)
}

// lanAddress accepts a numeric host:port on such an address.
func lanAddress(address string) error {
	a, err := netip.ParseAddrPort(address)
	if err != nil || a.Port() == 0 || a.Addr().Zone() != "" || !lanAddr(a.Addr()) {
		return hostedError("LAN relay address", "a numeric private-network address and port, such as 192.168.1.20:39032")
	}
	return nil
}

// LANAddresses lists this computer's private-network IPv4 addresses, which
// players nearby type to reach a LAN relay on it.
func LANAddresses() []string {
	var out []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, addr := range addrs {
		prefix, err := netip.ParsePrefix(addr.String())
		if err != nil {
			continue
		}
		if a := prefix.Addr().Unmap(); a.Is4() && a.IsPrivate() {
			out = append(out, a.String())
		}
	}
	return out
}
