package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestBoundedChildOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	buffer := &boundedOutput{limit: 4, cancel: cancel}
	if n, e := buffer.Write([]byte("four")); e != nil || n != 4 {
		t.Fatal("rejected output within limit")
	}
	if _, e := buffer.Write([]byte("extra")); e == nil {
		t.Fatal("accepted oversized output")
	}
	if buffer.Len() != 4 || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("oversized child was not bounded and cancelled")
	}
}

func TestCopyCannotBypassChildOutputLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	buffer := &boundedOutput{limit: 4, cancel: cancel}
	reader := struct{ io.Reader }{strings.NewReader("oversized child response")}
	if _, e := io.Copy(buffer, reader); e == nil || buffer.Len() > 4 || ctx.Err() == nil {
		t.Fatal("io.Copy bypassed the subprocess output limit")
	}
}

func TestHealthAddress(t *testing.T) {
	for input, expected := range map[string]string{
		"": "127.0.0.1:8080", ":9000": "127.0.0.1:9000",
		"0.0.0.0:8080": "127.0.0.1:8080", "[::]:8080": "[::1]:8080",
		"localhost:8080": "localhost:8080",
	} {
		got, err := healthAddress(input)
		if err != nil || got != expected {
			t.Fatalf("%q: got %q, %v", input, got, err)
		}
	}
}
