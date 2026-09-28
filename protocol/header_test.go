package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestMarshalGoldenBigEndian(t *testing.T) {
	h := Header{
		Version:  Version,
		Type:     TypeData,
		Flags:    FlagMAC,
		RoomID:   0x2a,
		SrcPeer:  1,
		DstPeer:  2,
		Sequence: 9,
		Reserved: 0,
	}
	got, err := Marshal(h, []byte("test"))
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		'G', 'L', 'N', 'K',
		0x01, 0x01,
		0x00, 0x01,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x2a,
		0x00, 0x00, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x02,
		0x00, 0x00, 0x00, 0x09,
		0x00, 0x04,
		0x00, 0x00,
		't', 'e', 's', 't',
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("bytes\n got %x\nwant %x", got, want)
	}
	if binary.BigEndian.Uint16(got[6:8]) != FlagMAC {
		t.Fatal("flags")
	}
	if !MACPresent(FlagMAC) || MACPresent(0) {
		t.Fatal("MACPresent")
	}
}

func TestRoundTrip(t *testing.T) {
	cases := []Header{
		{Version: Version, Type: TypeData, RoomID: 1, SrcPeer: 1, DstPeer: 2, Sequence: 7},
		{Version: Version, Type: TypePing, Flags: FlagMAC, RoomID: ^uint64(0), SrcPeer: 8, DstPeer: 0, Sequence: 1 << 31, Reserved: 0xabcd},
		{Version: Version, Type: TypePong, RoomID: 99, SrcPeer: 0, DstPeer: 3},
		{Version: Version, Type: TypeHandshake, Flags: FlagMAC | 0x0002, RoomID: 5, SrcPeer: 2, DstPeer: 0},
	}
	payloads := [][]byte{nil, []byte("x"), bytes.Repeat([]byte{0xab}, 1200)}
	for _, h := range cases {
		for _, p := range payloads {
			buf, err := Marshal(h, p)
			if err != nil {
				t.Fatal(err)
			}
			if h.Flags&FlagMAC != 0 {
				buf = append(buf, bytes.Repeat([]byte{0x11}, MACSize)...)
			}
			got, payload, mac, err := Decode(buf)
			if err != nil {
				t.Fatal(err)
			}
			got.PayloadLen = 0
			want := h
			want.PayloadLen = 0
			if got != want {
				t.Fatalf("header %+v want %+v", got, want)
			}
			if !bytes.Equal(payload, p) && !(len(p) == 0 && len(payload) == 0) {
				t.Fatalf("payload %q want %q", payload, p)
			}
			if MACPresent(h.Flags) {
				if len(mac) != MACSize {
					t.Fatalf("mac len %d", len(mac))
				}
			} else if mac != nil {
				t.Fatal("unexpected mac")
			}
		}
	}
}

func TestDecodeErrors(t *testing.T) {
	good, err := Marshal(Header{Version: Version, Type: TypePing, RoomID: 1, SrcPeer: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := Decode(good[:10]); err != ErrShort {
		t.Fatalf("short: %v", err)
	}
	badMagic := append([]byte(nil), good...)
	badMagic[0] = 'X'
	if _, _, _, err := Decode(badMagic); err != ErrMagic {
		t.Fatalf("magic: %v", err)
	}
	badVer := append([]byte(nil), good...)
	badVer[4] = 2
	if _, _, _, err := Decode(badVer); err != ErrVersion {
		t.Fatalf("version: %v", err)
	}
	trailing := append(append([]byte(nil), good...), 0xff)
	if _, _, _, err := Decode(trailing); err != ErrTrailing {
		t.Fatalf("trailing: %v", err)
	}
	withMAC := append([]byte(nil), good...)
	binary.BigEndian.PutUint16(withMAC[6:8], FlagMAC)
	if _, _, _, err := Decode(withMAC); err != ErrShort {
		t.Fatalf("mac missing: %v", err)
	}
	if _, err := Marshal(Header{Version: 2, Type: TypePing}, nil); err != ErrVersion {
		t.Fatalf("marshal version: %v", err)
	}
	if _, err := Marshal(Header{Version: Version}, nil); err != ErrType {
		t.Fatalf("marshal type: %v", err)
	}
}

func TestPayloadLenExcludesMAC(t *testing.T) {
	h := Header{Version: Version, Type: TypeData, Flags: FlagMAC, RoomID: 3, SrcPeer: 1, DstPeer: 2, Sequence: 4}
	payload := []byte{1, 2, 3, 4, 5}
	buf, err := Marshal(h, payload)
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint16(buf[28:30]) != uint16(len(payload)) {
		t.Fatal("payload_len")
	}
	buf = append(buf, make([]byte, MACSize)...)
	got, p, mac, err := Decode(buf)
	if err != nil {
		t.Fatal(err)
	}
	if int(got.PayloadLen) != len(payload) || len(p) != len(payload) || len(mac) != MACSize {
		t.Fatalf("split payload=%d mac=%d", len(p), len(mac))
	}
	if len(buf) != HeaderSize+len(payload)+MACSize {
		t.Fatal(len(buf))
	}
}
