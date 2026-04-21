package capture

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
)

// ProxyStdio spawns name+args as a subprocess and shuttles stdio between the
// parent (os.Stdin/Stdout) and the child, recording every newline-delimited
// frame in both directions to rec. Child stderr is passed through.
func ProxyStdio(name string, args []string, rec *Recorder) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = os.Stderr

	childIn, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	childOut, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	rec.Event("connect", map[string]string{
		"command": name,
		"pid":     fmt.Sprint(cmd.Process.Pid),
	})

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		if err := pumpStdio(os.Stdin, childIn, DirC2S, rec); err != nil {
			rec.Event("error", map[string]string{"dir": "c2s", "err": err.Error()})
		}
		childIn.Close()
	}()

	go func() {
		defer wg.Done()
		if err := pumpStdio(childOut, os.Stdout, DirS2C, rec); err != nil {
			rec.Event("error", map[string]string{"dir": "s2c", "err": err.Error()})
		}
	}()

	wg.Wait()
	waitErr := cmd.Wait()

	meta := map[string]string{}
	if waitErr != nil {
		meta["err"] = waitErr.Error()
	}
	if cmd.ProcessState != nil {
		meta["exit_code"] = fmt.Sprint(cmd.ProcessState.ExitCode())
	}
	rec.Event("disconnect", meta)
	return waitErr
}

func pumpStdio(src io.Reader, dst io.Writer, dir Direction, rec *Recorder) error {
	buf := bufio.NewReaderSize(src, 64*1024)
	for {
		line, err := buf.ReadBytes('\n')
		if len(line) > 0 {
			payload := bytes.TrimRight(line, "\r\n")
			var meta map[string]string
			if errors.Is(err, io.EOF) && !bytes.HasSuffix(line, []byte{'\n'}) {
				meta = map[string]string{"partial": "true"}
			}
			if len(payload) > 0 {
				rec.Frame(dir, TransportStdio, payload, meta)
			}
			if _, werr := dst.Write(line); werr != nil {
				return werr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
