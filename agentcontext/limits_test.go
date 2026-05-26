package agentcontext

import "testing"

func TestEstimateTokens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int
		want int
	}{
		{0, 0},
		{-1, 0},
		{1, 1},
		{3, 1},
		{4, 1},
		{5, 2},
		{8, 2},
		{9, 3},
		{1024, 256},
		{1025, 257},
	}
	for _, tc := range cases {
		if got := EstimateTokens(tc.in); got != tc.want {
			t.Errorf("EstimateTokens(%d) = %d; want %d", tc.in, got, tc.want)
		}
	}
}
