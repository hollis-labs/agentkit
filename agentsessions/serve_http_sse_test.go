package agentsessions

import (
	"reflect"
	"strings"
	"testing"
)

// readSSEData follows the WHATWG event-stream rules for the data field:
// successive data lines join with "\n", one leading space is stripped,
// other fields and comments are ignored, an event without data is not
// dispatched, and an event the stream ends before terminating is dropped.
func TestReadSSEData(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire string
		want []string
	}{
		{"single line", "data: {\"a\":1}\n\n", []string{`{"a":1}`}},
		{"multi-line joins with newline", "data: a\ndata: b\n\n", []string{"a\nb"}},
		{"three lines", "data: a\ndata: b\ndata: c\n\n", []string{"a\nb\nc"}},
		{"empty data line keeps its newline", "data: a\ndata:\ndata: b\n\n", []string{"a\n\nb"}},
		{"bare data field", "data\ndata: b\n\n", []string{"\nb"}},
		{"no space after colon", "data:x\n\n", []string{"x"}},
		{"only one leading space stripped", "data:  x\n\n", []string{" x"}},
		{"CRLF line endings", "data: a\r\ndata: b\r\n\r\n", []string{"a\nb"}},
		{"other fields and comments ignored", ": ping\nevent: message\nid: 7\ndata: a\nretry: 10\n\n", []string{"a"}},
		{"events split by blank lines", "data: a\n\ndata: b\n\n", []string{"a", "b"}},
		{"event without data not dispatched", "event: ping\n\ndata: a\n\n", []string{"a"}},
		{"unterminated event dropped at EOF", "data: a\n\ndata: b\n", []string{"a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			readSSEData(strings.NewReader(tc.wire), func(b []byte) { got = append(got, string(b)) })
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("readSSEData(%q) = %q, want %q", tc.wire, got, tc.want)
			}
		})
	}
}
