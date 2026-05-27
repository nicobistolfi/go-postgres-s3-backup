package backup

import "fmt"

// HumanizeSize formats a byte count using binary units (KB/MB/GB/...), or
// plain bytes below 1 KB. For example HumanizeSize(1572864) returns "1.50 MB".
func HumanizeSize(b int) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := int64(b) / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
