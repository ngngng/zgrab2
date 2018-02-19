package oracle

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
)

// References:
// https://wiki.wireshark.org/Oracle
// https://blog.pythian.com/repost-oracle-protocol/
// http://www.nyoug.org/Presentations/2008/Sep/Harris_Listening%20In.pdf

type PacketType uint8

const (
	PacketTypeConnect            PacketType = 1
	PacketTypeAccept                        = 2
	PacketTypeAcknowledge                   = 3
	PacketTypeRefuse                        = 4
	PacketTypeRedirect                      = 5
	PacketTypeData                          = 6
	PacketTypeNull                          = 7
	PacketTypeAbort                         = 9
	PacketTypeResend                        = 11
	PacketTypeMarker                        = 12
	PacketTypeAttention                     = 13
	PacketTypeControlInformation            = 14
)

var (
	ErrInvalidData error = errors.New("server returned invalid data")
)

// Implementation of io.Reader that returns data from a slice.
// Lets Decode methods re-use Read methods.
type sliceReader struct {
	Data []byte
}

func getSliceReader(data []byte) *sliceReader {
	return &sliceReader{Data: data}
}

func getStack() string {
	v := string(debug.Stack())
	parts := strings.Split(v, "\n")
	ret := make([]string, 0)
	for _, v := range parts {
		if !strings.Contains(v, "/Go/src/") {
			// c:/Go/src
			a := strings.LastIndex(v, "/")
			if a != -1 {
				s := v[a+1:]
				b := strings.IndexAny(s, " (")
				if b != -1 {
					val := s[:b]
					if strings.Contains(val, ".go") {
						ret = append(ret, val)
					}
				}
			}
		}
	}
	return strings.Join(ret, ", ")
}

func (reader *sliceReader) Read(output []byte) (int, error) {
	if reader.Data == nil {
		return 0, io.EOF
	}
	n := len(output)
	if n > len(reader.Data) {
		n = len(reader.Data)
	}
	copy(output[0:n], reader.Data[0:n])
	reader.Data = reader.Data[n:]
	return n, nil
}

var (
	ErrBufferTooSmall error = errors.New("buffer too small")
)

type TNSFlags uint8

type TNSHeader struct {
	// 00..01
	Length uint16
	// 02..03
	PacketChecksum uint16
	// 04
	Type PacketType
	// 05
	Flags TNSFlags
	// 06..07
	HeaderChecksum uint16
}

func (header *TNSHeader) EncodeTo(ret []byte) []byte {
	// 1 g m/s^2 / m^2
	// | . | 0 1 0 -- p + q = P + Q
	if ret == nil {
		ret = make([]byte, 8)
	}
	next := ret
	next = pushU16(ret, header.Length)
	next = pushU16(next, header.PacketChecksum)
	next = pushU8(next, byte(header.Type))
	next = pushU8(next, byte(header.Flags))
	next = pushU16(next, header.HeaderChecksum)
	return ret
}

func (header *TNSHeader) Encode() []byte {
	return header.EncodeTo(nil)
}

func DecodeTNSHeader(ret *TNSHeader, buf []byte) (*TNSHeader, []byte, error) {
	if len(buf) < 8 {
		return nil, nil, ErrBufferTooSmall
	}
	if ret == nil {
		ret = new(TNSHeader)
	}
	var u8 uint8
	rest := buf
	ret.Length, rest = popU16(rest)
	ret.PacketChecksum, rest = popU16(rest)
	u8, rest = popU8(rest)
	ret.Type = PacketType(u8)
	u8, rest = popU8(rest)
	ret.Flags = TNSFlags(u8)
	ret.HeaderChecksum, rest = popU16(rest)
	return ret, rest, nil
}

func ReadTNSHeader(reader io.Reader) (*TNSHeader, error) {
	buf := make([]byte, 8)
	_, err := io.ReadFull(reader, buf)
	ret, _, err := DecodeTNSHeader(nil, buf)
	return ret, err
}

// Flags taken from Wireshark

type ServiceOptions uint16

const (
	SOBrokenConnectNotify ServiceOptions = 0x2000
	SOPacketChecksum                     = 0x1000
	SOHeaderChecksum                     = 0x0800
	SOFullDuplex                         = 0x0400
	SOHalfDuplex                         = 0x0200
	SOUnknown0100                        = 0x0100
	SOUnknown0080                        = 0x0080
	SOUnknown0040                        = 0x0040
	SOUnknown0020                        = 0x0020
	SODirectIO                           = 0x0010
	SOAttentionProcessing                = 0x0008
	SOCanReceiveAttention                = 0x0004
	SOCanSendAttention                   = 0x0002
	SOUnknown0001                        = 0x0001
)

type NTProtocolCharacteristics uint16

const (
	NTPCHangon           NTProtocolCharacteristics = 0x8000
	NTPCConfirmedRelease                           = 0x4000
	NTPCTDUBasedIO                                 = 0x2000
	NTPCSpawnerRunning                             = 0x1000
	NTPCDataTest                                   = 0x0800
	NTPCCallbackIO                                 = 0x0400
	NTPCAsyncIO                                    = 0x0200
	NTPCPacketIO                                   = 0x0100
	NTPCCanGrant                                   = 0x0080
	NTPCCanHandoff                                 = 0x0040
	NTPCGenerateSIGIO                              = 0x0020
	NTPCGenerateSIGPIPE                            = 0x0010
	NTPCGenerateSIGURG                             = 0x0008
	NTPCUrgentIO                                   = 0x0004
	NTPCFullDuplex                                 = 0x0002
	NTPCTestOperation                              = 0x0001
)

type ConnectFlags uint8

const (
	CFServicesRequired    ConnectFlags = 0x10
	CFServicesLinkedIn                 = 0x08
	CFServicesEnabled                  = 0x04
	CFInterchangeInvolved              = 0x02
	CFServicesWanted                   = 0x01
	CFUnknown80                        = 0x80
	CFUnknown40                        = 0x40
	CFUnknown20                        = 0x20
)

// DefaultByteOrder is the little-endian encoding of the uint16 integer 1 --
// the server takes this value in some packets.
var DefaultByteOrder = [2]byte{1, 0}

// If len(packet) > 255, send a packet with data="", followed by data
type TNSConnect struct {
	// 08..09: 0x0136 / 0x0134?
	// TODO: Find Version format (10r2 = 0x0139? 9r2 = 0x0138? 9i = 0x0137? 8 = 0x0136?)
	Version uint16
	// 0A..0B: 0x012c? 0x013b?
	MinVersion uint16
	// 0C..0D: 0x0c01
	GlobalServiceOptions ServiceOptions
	// 0E..0F: 0x0800
	SDU uint16
	// 10..11: 0x7fff
	TDU uint16
	// 12..13: 0x4380 / 0x4f98
	ProtocolCharacteristics NTProtocolCharacteristics
	// 14..15: 0
	MaxBeforeAck uint16
	// 16..17: 01 00
	ByteOrder [2]byte
	// 18..19: 0x0081
	DataLength uint16
	// 1A..1B: 0x003a? Found to be 0x0046..
	DataOffset uint16
	// 1C..1F: 0x0000 0800
	MaxResponseSize uint32

	// 20..21: 0x0101
	ConnectFlags0 ConnectFlags
	ConnectFlags1 ConnectFlags
	// 22..25: 0x0000 0000
	CrossFacility0 uint32
	// 26..29: 0x0000 0000
	CrossFacility1 uint32
	// 2A..31: 00 00 7b 8b  00 00 00 18
	ConnectionID0 [8]byte
	// 32..39: 00 00 00 00  00 00 00 00
	ConnectionID1 [8]byte
	// Unknown3A is the data between the last trace unique connection ID and the
	// connection string, starting from offset 0x3A.
	// The DataOffset points past this, and the DataLength counts from there, so
	// this is indeed part of the "header".
	// On recent versions of MSSQL this is 12 bytes.
	// On older versions, it is 0 bytes.
	Unknown3A []byte
	// (DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=target)(Port=1521))(CONNECT_DATA=(SID=thesid)(CID=(PROGRAM=)(HOST=me)(USER=theuser))))
	ConnectionString string
}

func (header *TNSHeader) String() string {
	ret, err := json.Marshal(*header)
	if err != nil {
		return fmt.Sprintf("(error encoding %v: %v)", header, err)
	}
	return string(ret)
}

func push(dest []byte, data []byte) []byte {
	copy(dest[0:len(data)], data)
	return dest[len(data):]
}

func pushU16(dest []byte, v uint16) []byte {
	binary.BigEndian.PutUint16(dest[0:2], v)
	return dest[2:]
}

func pushU32(dest []byte, v uint32) []byte {
	binary.BigEndian.PutUint32(dest[0:4], v)
	return dest[4:]
}

func pushU8(dest []byte, v uint8) []byte {
	dest[0] = v
	return dest[1:]
}

func pushSZ(dest []byte, s string) []byte {
	dest = push(dest, []byte(s))
	return pushU8(dest, 0)
}

func (packet *TNSConnect) Encode() []byte {
	length := 0x3A + len(packet.Unknown3A) + len(packet.ConnectionString)
	if length > 255 {
		temp := packet.ConnectionString
		defer func() {
			packet.ConnectionString = temp
		}()
		packet.ConnectionString = ""
		return append(packet.Encode(), []byte(temp)...)
	}

	ret := make([]byte, length-8)
	next := ret
	next = pushU16(next, packet.Version)
	next = pushU16(next, packet.MinVersion)
	next = pushU16(next, uint16(packet.GlobalServiceOptions))
	next = pushU16(next, packet.SDU)
	next = pushU16(next, packet.TDU)
	next = pushU16(next, uint16(packet.ProtocolCharacteristics))
	next = pushU16(next, packet.MaxBeforeAck)
	next = push(next, packet.ByteOrder[:])
	next = pushU16(next, packet.DataLength)
	next = pushU16(next, packet.DataOffset)
	next = pushU32(next, packet.MaxResponseSize)
	next = pushU8(next, uint8(packet.ConnectFlags0))
	next = pushU8(next, uint8(packet.ConnectFlags1))
	next = pushU32(next, packet.CrossFacility0)
	next = pushU32(next, packet.CrossFacility1)
	next = push(next, packet.ConnectionID0[:])
	next = push(next, packet.ConnectionID1[:])
	next = push(next, packet.Unknown3A)
	push(next, []byte(packet.ConnectionString))
	return ret
}

func (header *TNSConnect) String() string {
	ret, err := json.Marshal(*header)
	if err != nil {
		return fmt.Sprintf("(error encoding %v: %v)", header, err)
	}
	return string(ret)
}

func unpanic() error {
	if rerr := recover(); rerr != nil {
		switch err := rerr.(type) {
		case error:
			return err
		default:
			panic(rerr)
		}
	}
	return nil
}

func ReadTNSConnect(reader io.Reader, header *TNSHeader) (ret *TNSConnect, thrown error) {
	defer func() {
		if err := unpanic(); err != nil {
			thrown = err
		}
	}()
	ret = new(TNSConnect)
	ret.Version = readU16(reader)
	ret.MinVersion = readU16(reader)
	ret.GlobalServiceOptions = ServiceOptions(readU16(reader))
	ret.SDU = readU16(reader)
	ret.TDU = readU16(reader)
	ret.ProtocolCharacteristics = NTProtocolCharacteristics(readU16(reader))
	ret.MaxBeforeAck = readU16(reader)
	if _, err := io.ReadFull(reader, ret.ByteOrder[:]); err != nil {
		return nil, err
	}
	ret.DataLength = readU16(reader)
	ret.DataOffset = readU16(reader)
	ret.MaxResponseSize = readU32(reader)
	ret.ConnectFlags0 = ConnectFlags(readU8(reader))
	ret.ConnectFlags1 = ConnectFlags(readU8(reader))
	ret.CrossFacility0 = readU32(reader)
	ret.CrossFacility1 = readU32(reader)
	if _, err := io.ReadFull(reader, ret.ConnectionID0[:]); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(reader, ret.ConnectionID1[:]); err != nil {
		return nil, err
	}
	unknownLen := ret.DataOffset - 0x3A
	ret.Unknown3A = make([]byte, unknownLen)
	if _, err := io.ReadFull(reader, ret.Unknown3A); err != nil {
		return nil, err
	}
	data := make([]byte, ret.DataLength)
	if _, err := io.ReadFull(reader, data); err != nil {
		return nil, err
	}
	ret.ConnectionString = string(data)
	return ret, nil
}

// TNSResend is just a header with type = PacketTypeResend (0x0b == 11)
type TNSResend struct {
}

func (packet *TNSResend) Encode() []byte {
	return []byte{}
}

func ReadTNSResend(reader io.Reader, header *TNSHeader) (*TNSResend, error) {
	ret := TNSResend{}
	return &ret, nil
}

// TODO: TNSConnect.Decode()

type TNSAccept struct {
	// 08..09: 0x0136 / 0x0134?
	Version uint16
	// 0A..0B: 0x0801
	GlobalServiceOptions ServiceOptions
	// 0C..0D: 0x0800
	SDU uint16
	// 0E..0F: 0x7fff
	TDU uint16
	// 10..11: 01 00
	ByteOrder [2]byte
	// 12..13: 0x0000
	DataLength uint16
	// 14..15: 0x0020
	DataOffset uint16
	// 16..17: 0x0101
	ConnectFlags0 ConnectFlags
	ConnectFlags1 ConnectFlags

	// Unknown18 provides support for case like TNSConnect, where there is
	// "data" after the end of the known packet but before the start of the
	// AcceptData pointed to by DataOffset.
	// Currently this is always 8 bytes.
	Unknown18  []byte
	AcceptData []byte
}

func popU8(buf []byte) (uint8, []byte) {
	return uint8(buf[0]), buf[1:]
}

func popU16(buf []byte) (uint16, []byte) {
	return binary.BigEndian.Uint16(buf[0:2]), buf[2:]
}

func popU32(buf []byte) (uint32, []byte) {
	return binary.BigEndian.Uint32(buf[0:4]), buf[4:]
}

func popN(buf []byte, n int) ([]byte, []byte) {
	return buf[0:n], buf[n:]
}

func (packet *TNSAccept) Encode() []byte {
	length := 16 + len(packet.Unknown18) + len(packet.AcceptData)
	ret := make([]byte, length)
	next := ret
	next = pushU16(next, packet.Version)
	next = pushU16(next, uint16(packet.GlobalServiceOptions))
	next = pushU16(next, packet.SDU)
	next = pushU16(next, packet.TDU)
	next = push(next, packet.ByteOrder[:])
	// packet.DataLength = len(packet.AcceptData)
	// packet.DataOffset = 8 + 16 + len(packet.Unknown18) // TNSHeader + accept header + unknown
	next = pushU16(next, packet.DataLength)
	next = pushU16(next, packet.DataOffset)
	next = pushU8(next, uint8(packet.ConnectFlags0))
	next = pushU8(next, uint8(packet.ConnectFlags1))
	next = push(next, packet.Unknown18)
	copy(next, packet.AcceptData)
	return ret
}

func readU8(reader io.Reader) uint8 {
	buf := make([]byte, 1)
	_, err := io.ReadFull(reader, buf)
	if err != nil {
		panic(err)
	}
	return buf[0]
}

func readU16(reader io.Reader) uint16 {
	buf := make([]byte, 2)
	_, err := io.ReadFull(reader, buf)
	if err != nil {
		panic(err)
	}
	return binary.BigEndian.Uint16(buf)
}

func readU32(reader io.Reader) uint32 {
	buf := make([]byte, 4)
	_, err := io.ReadFull(reader, buf)
	if err != nil {
		panic(err)
	}
	return binary.BigEndian.Uint32(buf)
}

func ReadTNSAccept(reader io.Reader, header *TNSHeader) (ret *TNSAccept, thrown error) {
	defer func() {
		if err := unpanic(); err != nil {
			thrown = err
		}
	}()
	ret = new(TNSAccept)
	ret.Version = readU16(reader)
	ret.GlobalServiceOptions = ServiceOptions(readU16(reader))
	ret.SDU = readU16(reader)
	ret.TDU = readU16(reader)
	if _, err := io.ReadFull(reader, ret.ByteOrder[:]); err != nil {
		return nil, err
	}
	ret.DataLength = readU16(reader)
	ret.DataOffset = readU16(reader)
	ret.ConnectFlags0 = ConnectFlags(readU8(reader))
	ret.ConnectFlags1 = ConnectFlags(readU8(reader))
	unknownLen := ret.DataOffset - 16 - 8
	ret.Unknown18 = make([]byte, unknownLen)
	if _, err := io.ReadFull(reader, ret.Unknown18); err != nil {
		return nil, err
	}
	ret.AcceptData = make([]byte, ret.DataLength)
	if _, err := io.ReadFull(reader, ret.AcceptData); err != nil {
		return nil, err
	}
	return ret, nil
}

type RefuseReason uint8

type TNSRefuse struct {
	// 08: 01
	AppReason RefuseReason
	// 09: 00
	SysReason RefuseReason
	// 0A..0B: 0010
	DataLength uint16
	// 0C...
	Data []byte
}

type TNSRedirect struct {
	DataLength uint16
	Data       []byte
}

type DataFlags uint16

const (
	DFSendToken           DataFlags = 0x0001
	DFRequestConfirmation           = 0x0002
	DFConfirmation                  = 0x0004
	DFReserved                      = 0x0008
	DFUnknown0010                   = 0x0010
	DFMoreData                      = 0x0020
	DFEOF                           = 0x0040
	DFConfirmImmediately            = 0x0080
	DFRequestToSend                 = 0x0100
	DFSendNTTrailer                 = 0x0200
)

type TNSData struct {
	// 08..09
	DataFlags DataFlags
	// 0A..0B
	Unknown0A uint16
	// 0C
	TNSCounter uint8
	// 0D..0E
	Unknown0D uint16
}

type DataType uint8

const (
	DataTypeSetProtocol           DataType = 0x01
	DataTypeSecureNetworkServices          = 0x06
)

type TNSDataSetProtocolRequest struct {
	// 08..09
	DataFlags DataFlags
	// 0A
	DataType DataType
	// 0B...(null)
	AcceptedVersions []byte
	// ...
	ClientPlatform string
}

type TNSDataSetProtocolResponse struct {
	// 08..09
	DataFlags DataFlags
	// 0A
	DataType DataType
	// 0B...(null)
	AcceptedVersions []byte
	// ...(null)
	ServerBanner string
	// ...
	Data []byte
}

type TNSDataANOPacket struct {
	DataFlags     DataFlags
	DataType      DataType
	ClientVersion [4]byte
	Data          []byte
}

func (packet *TNSDataANOPacket) Encode() []byte {
	ret := make([]byte, 7+len(packet.Data))
	next := ret
	next = pushU16(next, uint16(packet.DataFlags))
	next = pushU8(next, uint8(packet.DataType))
	copy(next, packet.ClientVersion[0:4])
	copy(next[4:], packet.Data[:])
	return ret
}

func ReadTNSDataANOPacket(reader io.Reader, header *TNSHeader) (ret *TNSDataANOPacket, thrown error) {
	defer func() {
		rerr := recover()
		if rerr != nil {
			switch err := rerr.(type) {
			case error:
				thrown = err
			default:
				panic(rerr)
			}
		}
	}()
	ret = new(TNSDataANOPacket)
	ret.DataFlags = DataFlags(readU16(reader))
	ret.DataType = DataType(readU8(reader))
	if _, err := io.ReadFull(reader, ret.ClientVersion[:]); err != nil {
		return nil, err
	}
	ret.Data = make([]byte, header.Length-8-7)
	if _, err := io.ReadFull(reader, ret.Data); err != nil {
		return nil, err
	}
	return ret, nil
}

type TNSPacketBody interface {
	Encode() []byte
}

type TNSPacket struct {
	Header *TNSHeader
	Body   TNSPacketBody
}

func (packet *TNSPacket) Encode() []byte {
	header := packet.Header.Encode()
	body := packet.Body.Encode()
	return append(header, body...)
}

func ReadTNSPacket(reader io.Reader) (*TNSPacket, error) {
	var body TNSPacketBody
	var err error

	header, err := ReadTNSHeader(reader)
	if err != nil {
		return nil, err
	}
	switch header.Type {
	case PacketTypeConnect:
		body, err = ReadTNSConnect(reader, header)
	case PacketTypeAccept:
		body, err = ReadTNSAccept(reader, header)
	case PacketTypeResend:
		body, err = ReadTNSResend(reader, header)
	default:
		err = ErrInvalidData
	}
	return &TNSPacket{
		Header: header,
		Body:   body,
	}, err
}