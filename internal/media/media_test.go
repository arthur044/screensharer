package media

import "testing"

func TestSplitAndCodec(t *testing.T) {
	au := []byte{
		0, 0, 0, 1, 0x09, 0xf0, // AUD
		0, 0, 0, 1, 0x67, 0x42, 0xc0, 0x1f, 0xaa, // SPS baseline 3.1
		0, 0, 1, 0x68, 0xce, // PPS (start code de 3 bytes)
		0, 0, 0, 1, 0x65, 0x88, 0x00, // IDR
	}
	nals := SplitNALs(au)
	if len(nals) != 4 {
		t.Fatalf("esperava 4 NALs, veio %d", len(nals))
	}
	if nals[1][0] != 0x67 || len(nals[1]) != 5 {
		t.Fatalf("SPS errado: % x", nals[1])
	}
	codec, ok := H264Codec(au)
	if !ok || codec != "avc1.42C01F" {
		t.Fatalf("codec = %q", codec)
	}
	if !HasIDR(au) {
		t.Fatal("IDR não detectado")
	}
	if HasIDR(au[:20]) {
		t.Fatal("IDR falso")
	}
}

func FuzzSplitNALs(f *testing.F) {
	f.Add([]byte{0, 0, 1, 0x67, 0, 0, 0, 1, 0x65})
	f.Fuzz(func(t *testing.T, b []byte) {
		for _, n := range SplitNALs(b) {
			if len(n) > len(b) {
				t.Fatal("NAL maior que a entrada")
			}
		}
		H264Codec(b)
		HasIDR(b)
	})
}
