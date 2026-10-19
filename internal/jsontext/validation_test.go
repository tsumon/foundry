package jsontext

import "testing"

func TestLosslessDistinguishesValidUnicodeFromDecoderRepair(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"query":"plain"}`),
		[]byte(`{"query":"\ud83d\ude00"}`),
		[]byte("{\"query\":\"\ufffd\"}"),
	} {
		if !Lossless(raw) {
			t.Errorf("rejected lossless JSON %q", raw)
		}
	}
	invalidUTF8 := append([]byte(`{"query":"`), append([]byte{0xff}, []byte(`"}`)...)...)
	for _, raw := range [][]byte{
		invalidUTF8,
		[]byte(`{"query":"\ud800"}`),
		[]byte(`{"query":"\udc00"}`),
		[]byte(`{"query":"\ud800x"}`),
	} {
		if Lossless(raw) {
			t.Errorf("accepted lossy JSON %q", raw)
		}
	}
}
