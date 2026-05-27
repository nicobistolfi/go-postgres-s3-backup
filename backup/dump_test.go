package backup

import "testing"

func TestRemoveTimestampComments(t *testing.T) {
	in := []byte("-- Started on 2026-05-27 10:00:00\n" +
		"CREATE TABLE foo (id int);\n" +
		"-- Completed on 2026-05-27 10:00:01\n" +
		"INSERT INTO foo VALUES (1);")
	want := "CREATE TABLE foo (id int);\nINSERT INTO foo VALUES (1);"

	if got := string(removeTimestampComments(in)); got != want {
		t.Errorf("removeTimestampComments() = %q, want %q", got, want)
	}
}

func TestRemoveTimestampCommentsNoComments(t *testing.T) {
	in := []byte("line one\nline two")
	if got := string(removeTimestampComments(in)); got != "line one\nline two" {
		t.Errorf("removeTimestampComments() altered content without timestamps: %q", got)
	}
}
