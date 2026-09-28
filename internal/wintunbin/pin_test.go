package wintunbin

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func TestPins(t *testing.T) {
	if len(ZipSHA256) != 64 || len(DLLSHA256) != 64 {
		t.Fatal("sha256 length")
	}
	if ZipSHA256 != "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51" {
		t.Fatal(ZipSHA256)
	}
	if Version != "0.14.1" || Arch != "amd64" {
		t.Fatalf("%s %s", Version, Arch)
	}
	if strings.Contains(DLLSHA256, "13") && strings.HasPrefix(Version, "0.13") {
		t.Fatal("refusing 0.13")
	}
}

func TestStagedDLLMatchesPin(t *testing.T) {
	b, err := os.ReadFile(FileName)
	if os.IsNotExist(err) {
		t.Skip("wintun.dll is staged by CI before the Windows build")
	}
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != DLLSHA256 {
		t.Fatalf("staged dll %x", sum)
	}
}
