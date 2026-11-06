package procfs

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"syscall"
)

// netlink and sock_diag constants from <linux/netlink.h>, <linux/sock_diag.h>
// and <linux/unix_diag.h>, which the syscall package does not define.
const (
	netlinkSockDiag  = 4
	sockDiagByFamily = 20
	udiagShowName    = 0x01
	udiagShowVFS     = 0x02
	udiagShowPeer    = 0x04
	unixDiagName     = 0
	unixDiagVFS      = 1
	unixDiagPeer     = 2
)

// UnixSockets lists every Unix socket in this network namespace, with the
// file each listening one is bound to. It asks the kernel directly, the way
// `ss -x` does, so no external tool is needed.
func UnixSockets() ([]UnixSocket, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, netlinkSockDiag)
	if err != nil {
		return nil, ErrNoSockDiag
	}
	defer syscall.Close(fd)
	if err := syscall.Bind(fd, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return nil, err
	}
	// Request: a netlink header, then struct unix_diag_req asking for every
	// state and for the name, the file and the peer of each socket.
	req := make([]byte, 16+24)
	binary.LittleEndian.PutUint32(req[0:], uint32(len(req)))                          // nlmsg_len
	binary.LittleEndian.PutUint16(req[4:], sockDiagByFamily)                          // nlmsg_type
	binary.LittleEndian.PutUint16(req[6:], syscall.NLM_F_REQUEST|syscall.NLM_F_DUMP)  // nlmsg_flags
	binary.LittleEndian.PutUint32(req[8:], 1)                                         // nlmsg_seq
	req[16] = syscall.AF_UNIX                                                         // sdiag_family
	binary.LittleEndian.PutUint32(req[20:], 0xffffffff)                               // udiag_states: all
	binary.LittleEndian.PutUint32(req[28:], udiagShowName|udiagShowVFS|udiagShowPeer) // udiag_show
	if err := syscall.Sendto(fd, req, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return nil, err
	}
	var out []UnixSocket
	buf := make([]byte, 1<<16)
	for {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			return nil, err
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			switch m.Header.Type {
			case syscall.NLMSG_DONE:
				return out, nil
			case syscall.NLMSG_ERROR:
				// The kernel answers ENOENT or EOPNOTSUPP when unix_diag is
				// absent; anything else is a real error worth its own name.
				if len(m.Data) >= 4 {
					errno := syscall.Errno(-int32(binary.LittleEndian.Uint32(m.Data[0:4])))
					if errno != syscall.ENOENT && errno != syscall.EOPNOTSUPP {
						return nil, fmt.Errorf("sock_diag: %w", errno)
					}
				}
				return nil, ErrNoSockDiag
			}
			if s, ok := parseUnixDiag(m.Data); ok {
				out = append(out, s)
			}
		}
	}
}

// parseUnixDiag reads one struct unix_diag_msg and the attributes after it.
func parseUnixDiag(b []byte) (UnixSocket, bool) {
	// unix_diag_msg is 16 bytes: family, type, state, pad, inode, cookie[2].
	if len(b) < 16 {
		return UnixSocket{}, false
	}
	s := UnixSocket{State: b[2], Inode: binary.LittleEndian.Uint32(b[4:])}
	// Attributes are (len uint16, type uint16, payload) padded to 4 bytes.
	for a := b[16:]; len(a) >= 4; {
		l := int(binary.LittleEndian.Uint16(a[0:]))
		t := binary.LittleEndian.Uint16(a[2:])
		if l < 4 || l > len(a) {
			break
		}
		p := a[4:l]
		switch t {
		case unixDiagName:
			// Abstract sockets start with a NUL; files are plain paths.
			s.Name = strings.TrimRight(string(p), "\x00")
		case unixDiagVFS:
			if len(p) >= 8 {
				s.VFSIno = binary.LittleEndian.Uint32(p[0:])
				s.VFSDev = binary.LittleEndian.Uint32(p[4:])
			}
		case unixDiagPeer:
			if len(p) >= 4 {
				s.Peer = binary.LittleEndian.Uint32(p[0:])
			}
		}
		next := (l + 3) &^ 3
		if next > len(a) {
			break
		}
		a = a[next:]
	}
	return s, true
}

// FileID returns the inode and the device of a file in the encoding sock_diag
// uses for UNIX_DIAG_VFS: the kernel's own dev_t (major << 20 | minor), which
// differs from the glibc layout stat(2) returns.
func FileID(path string) (ino, dev uint32, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, false
	}
	st, isStat := info.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, 0, false
	}
	// glibc's dev_t layout -> major and minor -> the kernel's 20-bit minor.
	major := uint32((st.Dev>>8)&0xfff) | uint32((st.Dev>>32)&^0xfff)
	minor := uint32(st.Dev&0xff) | uint32((st.Dev>>12)&^0xff)
	return uint32(st.Ino), major<<20 | minor, true
}
