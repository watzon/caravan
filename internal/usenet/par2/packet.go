package par2

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// The packet header is a fixed 64 bytes: an 8-byte magic sequence, an 8-byte
// total length, a 16-byte MD5 over everything from the recovery set ID to the
// end of the body, the 16-byte recovery set ID, and a 16-byte type.
const (
	packetHeaderSize = 64
	magicSize        = 8
	// maxBufferedPacket caps how much of a packet we will hold in memory.
	// Recovery slice packets are streamed regardless of size, so this only
	// bounds the metadata packets, none of which is legitimately large. A
	// corrupt length field cannot make us allocate a gigabyte.
	maxBufferedPacket = 32 << 20
)

var packetMagic = [magicSize]byte{'P', 'A', 'R', '2', 0, 'P', 'K', 'T'}

// Packet types. The spec fixes these as 16 ASCII bytes, zero-padded.
const (
	typeMain     = "PAR 2.0\x00Main\x00\x00\x00\x00"
	typeFileDesc = "PAR 2.0\x00FileDesc"
	typeIFSC     = "PAR 2.0\x00IFSC\x00\x00\x00\x00"
	typeRecovery = "PAR 2.0\x00RecvSlic"
	typeCreator  = "PAR 2.0\x00Creator\x00"
)

// rawPacket is one verified packet. Body is nil for recovery slice packets,
// whose payload is left on disk and described by DataOffset/DataLength; every
// other packet type is small and is read into memory.
type rawPacket struct {
	Path  string
	Type  string
	SetID [16]byte
	Body  []byte

	Exponent   uint32
	DataOffset int64
	DataLength int64
}

// scanPackets walks every packet in path and calls visit for each one whose
// MD5 checks out. Packets that fail their own MD5, packets with a structurally
// impossible length, and bytes that are not part of any packet are skipped
// silently: a par2 file is explicitly allowed to contain junk between packets,
// and a volume that is itself damaged should still contribute the packets that
// survived rather than take the whole set down with it.
//
// Reading stops without error at the end of the file, including a file that
// ends mid-packet.
func scanPackets(path string, visit func(*rawPacket) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("par2: stat %s: %w", path, err)
	}
	size := info.Size()

	r := bufio.NewReaderSize(f, 64<<10)
	pos := int64(0)

	// resync repositions both the file and the buffered reader. It only runs
	// when a packet header turns out to be nonsense, which for a well-formed
	// set is never.
	resync := func(to int64) error {
		if _, err := f.Seek(to, io.SeekStart); err != nil {
			return fmt.Errorf("par2: seek %s: %w", path, err)
		}
		r.Reset(f)
		pos = to
		return nil
	}

	for {
		start, err := findMagic(r, &pos)
		if err != nil {
			return nil // end of file: no more packets
		}

		var rest [packetHeaderSize - magicSize]byte
		if _, err := io.ReadFull(r, rest[:]); err != nil {
			return nil // truncated header at end of file
		}
		pos += int64(len(rest))

		length := binary.LittleEndian.Uint64(rest[0:8])
		var hash, setID, ptype [16]byte
		copy(hash[:], rest[8:24])
		copy(setID[:], rest[24:40])
		copy(ptype[:], rest[40:56])

		if length < packetHeaderSize || length%4 != 0 || start+int64(length) > size {
			if err := resync(start + 1); err != nil {
				return err
			}
			continue
		}
		bodyLen := int64(length) - packetHeaderSize

		sum := md5.New()
		sum.Write(setID[:])
		sum.Write(ptype[:])

		pkt := &rawPacket{Path: path, Type: string(ptype[:]), SetID: setID}
		typeStr := pkt.Type

		switch {
		case typeStr == typeRecovery:
			if bodyLen < 4 {
				if err := resync(start + 1); err != nil {
					return err
				}
				continue
			}
			var exp [4]byte
			if _, err := io.ReadFull(r, exp[:]); err != nil {
				return nil
			}
			pos += 4
			sum.Write(exp[:])
			pkt.Exponent = binary.LittleEndian.Uint32(exp[:])
			pkt.DataOffset = pos
			pkt.DataLength = bodyLen - 4
			if _, err := io.CopyN(sum, r, pkt.DataLength); err != nil {
				return nil
			}
			pos += pkt.DataLength

		case bodyLen > maxBufferedPacket:
			// Not a recovery slice and implausibly large: step over it rather
			// than read it into memory.
			if err := resync(start + int64(length)); err != nil {
				return err
			}
			continue

		default:
			body := make([]byte, bodyLen)
			if _, err := io.ReadFull(r, body); err != nil {
				return nil
			}
			pos += bodyLen
			sum.Write(body)
			pkt.Body = body
		}

		if !bytes.Equal(sum.Sum(nil), hash[:]) {
			continue // damaged packet; the set may still be usable without it
		}
		if err := visit(pkt); err != nil {
			return err
		}
	}
}

// findMagic advances the reader to just past the next magic sequence and
// returns the absolute offset the sequence started at. It reads a byte at a
// time because a par2 file is only guaranteed to have its packets *somewhere*,
// not at a predictable alignment; in practice the very first read matches and
// this never scans.
func findMagic(r *bufio.Reader, pos *int64) (int64, error) {
	var window [magicSize]byte
	n := 0
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		*pos++
		if n < magicSize {
			window[n] = b
			n++
		} else {
			copy(window[:], window[1:])
			window[magicSize-1] = b
		}
		if n == magicSize && window == packetMagic {
			return *pos - magicSize, nil
		}
	}
}

// mainPacket is the spine of a set: the slice size and the ordered list of
// file IDs. Slices are numbered globally across the recovery set files in the
// order those IDs appear here, and that numbering is what assigns each slice
// its Reed-Solomon constant.
type mainPacket struct {
	SliceSize   uint64
	Recovery    [][16]byte
	NonRecovery [][16]byte
}

func parseMain(body []byte) (*mainPacket, error) {
	if len(body) < 12 {
		return nil, fmt.Errorf("%w: main packet is %d bytes", ErrMalformed, len(body))
	}
	m := &mainPacket{SliceSize: binary.LittleEndian.Uint64(body[0:8])}
	count := binary.LittleEndian.Uint32(body[8:12])

	ids := body[12:]
	if len(ids)%16 != 0 {
		return nil, fmt.Errorf("%w: main packet file id list is %d bytes", ErrMalformed, len(ids))
	}
	total := len(ids) / 16
	if int(count) > total {
		return nil, fmt.Errorf("%w: main packet claims %d recovery files but carries %d ids", ErrMalformed, count, total)
	}
	for i := 0; i < total; i++ {
		var id [16]byte
		copy(id[:], ids[i*16:])
		if i < int(count) {
			m.Recovery = append(m.Recovery, id)
		} else {
			m.NonRecovery = append(m.NonRecovery, id)
		}
	}

	if m.SliceSize == 0 || m.SliceSize%4 != 0 {
		return nil, fmt.Errorf("%w: slice size %d is not a positive multiple of 4", ErrMalformed, m.SliceSize)
	}
	return m, nil
}

// fileDesc is one file description packet: everything needed to recognise a
// file and to check it once it has been rebuilt.
type fileDesc struct {
	ID     [16]byte
	MD5    [16]byte
	MD5_16 [16]byte
	Length uint64
	Name   string
}

func parseFileDesc(body []byte) (*fileDesc, error) {
	if len(body) < 56 {
		return nil, fmt.Errorf("%w: file description packet is %d bytes", ErrMalformed, len(body))
	}
	d := &fileDesc{Length: binary.LittleEndian.Uint64(body[48:56])}
	copy(d.ID[:], body[0:16])
	copy(d.MD5[:], body[16:32])
	copy(d.MD5_16[:], body[32:48])

	// The name is ASCII, not NUL-terminated, and zero-padded to a multiple of
	// four. Trailing NULs are padding and are not part of the name.
	name := bytes.TrimRight(body[56:], "\x00")
	d.Name = string(name)
	if d.Name == "" {
		return nil, fmt.Errorf("%w: file description packet has an empty name", ErrMalformed)
	}

	// The file ID is defined as the MD5 of the last three fields of this
	// packet. Checking it is what stops two sets, or a set and a forged
	// packet, from being spliced together: the ID is the only thing tying a
	// slice checksum packet to a name.
	sum := md5.New()
	sum.Write(d.MD5_16[:])
	sum.Write(body[48:56])
	sum.Write(name)
	if !bytes.Equal(sum.Sum(nil), d.ID[:]) {
		return nil, fmt.Errorf("%w: file id for %q does not match its description", ErrMalformed, d.Name)
	}
	return d, nil
}

// sliceHash is the per-slice MD5 and CRC32 pair from an IFSC packet. Both are
// computed over the slice zero-padded to the full slice size, which is why a
// short final slice still has a full-size checksum.
type sliceHash struct {
	MD5 [16]byte
	CRC uint32
}

func parseIFSC(body []byte) ([16]byte, []sliceHash, error) {
	var id [16]byte
	if len(body) < 16 {
		return id, nil, fmt.Errorf("%w: slice checksum packet is %d bytes", ErrMalformed, len(body))
	}
	copy(id[:], body[0:16])
	rest := body[16:]
	if len(rest)%20 != 0 {
		return id, nil, fmt.Errorf("%w: slice checksum list is %d bytes, not a multiple of 20", ErrMalformed, len(rest))
	}
	hashes := make([]sliceHash, len(rest)/20)
	for i := range hashes {
		off := i * 20
		copy(hashes[i].MD5[:], rest[off:off+16])
		hashes[i].CRC = binary.LittleEndian.Uint32(rest[off+16 : off+20])
	}
	return id, hashes, nil
}

// errNoMainPacket is returned by the loader when a file it was pointed at
// contains no main packet at all, which is how "this is not a par2 index
// file" is distinguished from "this par2 index file is broken".
var errNoMainPacket = errors.New("par2: no main packet")
