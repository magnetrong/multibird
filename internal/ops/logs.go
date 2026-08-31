package ops

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/magnetrong/multibird/internal/instance"
)

// Logs writes the tail of an instance's daemon log to w. With follow=true it
// keeps polling for appended data until ctx is done (dependency-free tail -f;
// handles truncation/rotation by reopening from the start).
func (e *Env) Logs(ctx context.Context, inst *instance.Instance, w io.Writer, tailBytes int64, follow bool) error {
	path := inst.DeriveParams(e.Store.Root, e.Store.RunDir).LogFile
	f, err := os.Open(path) //nolint:gosec // G304: path is derived from a ValidateName-checked instance name under our config root
	if err != nil {
		return fmt.Errorf("opening log for %q: %w — the log appears after the first `sudo multibird up %s`", inst.Name, err, inst.Name)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return err
	}
	offset := int64(0)
	if fi.Size() > tailBytes {
		offset = fi.Size() - tailBytes
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	if _, err := io.Copy(w, f); err != nil {
		return err
	}
	if !follow {
		return nil
	}

	pos, _ := f.Seek(0, io.SeekCurrent)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(500 * time.Millisecond):
		}
		fi, err := os.Stat(path)
		if err != nil {
			continue // file momentarily gone (rotation)
		}
		if fi.Size() < pos { // truncated/rotated: start over
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return err
			}
			pos = 0
		}
		n, err := io.Copy(w, f)
		if err != nil {
			return err
		}
		pos += n
	}
}
