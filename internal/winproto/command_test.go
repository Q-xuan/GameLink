package winproto

import "testing"

func TestOpenCommand(t *testing.T) {
	got := OpenCommand(`C:\Games\gamelink.exe`)
	want := `"C:\Games\gamelink.exe" "%1"`
	if got != want {
		t.Fatalf("%s", got)
	}
}
