package pdu

import "testing"

// Independently written SMS-DELIVER fixtures: no SMSC; international sender
// +12345678901; 2026-10-16 12:34:56; TP-UDL is octets for UCS2/8-bit and
// septets for GSM7. Hex payloads are not produced by the decoder under test.
func TestDecodeTextAndConcatenationFixtures(t *testing.T) {
	for _, tc := range []struct {
		name, hex, text string
		ref, total, seq int
	}{
		{"UCS2 Chinese", "00000B912143658709F1000862016121436500044F60597D", "你好", -1, 0, 0},
		{"UCS2 supplementary", "00000B912143658709F100086201612143650004D83DDE00", "😀", -1, 0, 0},
		{"GSM7", "00000B912143658709F100006201612143650005E8329BFD06", "hello", -1, 0, 0},
		{"concatenated UCS2", "00400B912143658709F10008620161214365000A0500037A02014F60597D", "你好", 122, 2, 1},
		{"binary", "00000B912143658709F100046201612143650002FF00", "<binary: ff00>", -1, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := Decode(tc.hex)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Sender != "+12345678901" || rec.Timestamp != "2026-10-16 12:34:56" || rec.Text != tc.text || rec.ConcatRef != tc.ref || rec.ConcatTot != tc.total || rec.ConcatSeq != tc.seq {
				t.Fatalf("decoded=%+v", rec)
			}
		})
	}
}

func TestDecodeRejectsTruncatedPayloadInsteadOfArchivingPartialSMS(t *testing.T) {
	for _, hex := range []string{
		"00000B912143658709F1000862016121436500044F60",     // 4 octets declared, only 2
		"00000B912143658709F100006201612143650005E8329BFD", // 5 septets need 5 octets
		"00000B912143658709F1000862016121436500034F6059",   // incomplete UTF-16 unit
		"00400B912143658709F1000862016121436500040500037A", // UDHL exceeds TP-UDL
		"00400B912143658709F100086201612143650000",         // UDH flag without a header
	} {
		if rec, err := Decode(hex); err == nil {
			t.Errorf("truncated PDU accepted: %s => %+v", hex, rec)
		}
	}
}
