package backup

import "testing"

func TestHumanizeSize(t *testing.T) {
	tests := []struct {
		bytes int
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{1048576, "1.00 MB"},
		{1572864, "1.50 MB"},
		{1073741824, "1.00 GB"},
		{5 * 1073741824, "5.00 GB"},
	}
	for _, tt := range tests {
		if got := HumanizeSize(tt.bytes); got != tt.want {
			t.Errorf("HumanizeSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}
