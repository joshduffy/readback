package output

import (
	"errors"
	"io"
	"testing"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestEmitWriteFailureIsNonzero(t *testing.T) {
	jsonWriter := New(failingWriter{}, io.Discard, true)
	if got := jsonWriter.Emit(Result{Command: "test", OK: true, Data: make(chan int)}, nil); got != ExitCouldNotCheck {
		t.Fatalf("JSON failure exit = %d", got)
	}

	humanWriter := New(failingWriter{}, io.Discard, false)
	humanWriter.JSON = false
	if got := humanWriter.Emit(Result{Command: "test", OK: true}, func(out io.Writer) {
		Table(out, []string{"RESULT"}, [][]string{{"ok"}})
	}); got != ExitCouldNotCheck {
		t.Fatalf("human failure exit = %d", got)
	}
}

func TestEmitWriteFailurePreservesNonzeroExit(t *testing.T) {
	for _, exit := range []int{ExitUnproven, ExitCouldNotCheck, ExitUsage} {
		writer := New(failingWriter{}, io.Discard, true)
		if got := writer.Emit(Result{Command: "test", Exit: exit}, nil); got != exit {
			t.Fatalf("exit %d became %d", exit, got)
		}
	}
}
