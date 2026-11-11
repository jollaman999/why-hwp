//go:build windows

package hancom

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

//go:embed convert.ps1
var convertScript []byte

// Convert converts o.In to o.Out with Hancom Office. The input is copied
// into a new folder under %TEMP% because Hwp shows a permission dialog when
// it saves outside the temporary folder. Only the Hwp instance started for
// this call is closed; on timeout only that process is killed.
func Convert(ctx context.Context, o Options) (res Result, err error) {
	openFmt, err := openFormat(o.In)
	if err != nil {
		return Result{}, err
	}
	saveFmt := o.To.saveFormat()
	if saveFmt == "" {
		return Result{}, fmt.Errorf("unknown output format %q", o.To)
	}
	if o.Timeout <= 0 {
		o.Timeout = 120 * time.Second
	}
	if !o.Force {
		if _, err := os.Lstat(o.Out); err == nil {
			return Result{}, fmt.Errorf("%s: already exists (use -force to overwrite)", o.Out)
		}
	}

	dir, err := os.MkdirTemp("", "why-hwp-convert-")
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if rerr := os.RemoveAll(dir); rerr != nil && err == nil {
			err = fmt.Errorf("remove work directory: %w", rerr)
		}
	}()
	in := filepath.Join(dir, "in"+strings.ToLower(filepath.Ext(o.In)))
	if err := copyFile(o.In, in, true); err != nil {
		return Result{}, err
	}
	tmpOut := filepath.Join(dir, "out."+string(o.To))
	script := filepath.Join(dir, "convert.ps1")
	if err := os.WriteFile(script, convertScript, 0o600); err != nil {
		return Result{}, err
	}

	reg, err := runScript(ctx, o.Timeout, script, map[string]string{
		"WHY_HWP_IN":          in,
		"WHY_HWP_OUT":         tmpOut,
		"WHY_HWP_OPEN_FORMAT": openFmt,
		"WHY_HWP_SAVE_FORMAT": saveFmt,
	})
	if err != nil {
		return Result{}, err
	}

	st, err := os.Stat(tmpOut)
	if err != nil {
		return Result{}, fmt.Errorf("Hwp reported success but wrote no output: %w", err)
	}
	if st.Size() == 0 {
		return Result{}, errors.New("Hwp wrote an empty output file")
	}
	want, err := md5File(tmpOut)
	if err != nil {
		return Result{}, err
	}
	if err := copyFile(tmpOut, o.Out, !o.Force); err != nil {
		return Result{}, err
	}
	got, err := md5File(o.Out)
	if err != nil {
		return Result{}, err
	}
	fst, err := os.Stat(o.Out)
	if err != nil {
		return Result{}, err
	}
	if got != want || fst.Size() != st.Size() {
		return Result{}, fmt.Errorf("%s: copy differs from the converted file (md5 %s, want %s)", o.Out, got, want)
	}
	return Result{Out: o.Out, Format: o.To, Size: fst.Size(), MD5: got, RegisterModule: reg}, nil
}

// runScript runs the conversion script and makes sure the Hwp instance it
// started is gone afterwards.
func runScript(ctx context.Context, timeout time.Duration, script string, env map[string]string) (reg bool, err error) {
	before, err := hwpPIDs()
	if err != nil {
		return false, err
	}
	cmd := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-File", script)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("start powershell: %w", err)
	}

	lines := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var mine int
	timedOut := false
read:
	for {
		select {
		case s, ok := <-lines:
			if !ok {
				break read
			}
			if p, ok := parsePIDLine(s); ok {
				mine = p
			}
			if v, found := strings.CutPrefix(s, "registermodule "); found {
				reg = strings.EqualFold(strings.TrimSpace(v), "true")
			}
		case <-tctx.Done():
			timedOut = true
			_ = cmd.Process.Kill()
			go func() {
				for range lines {
				}
			}()
			break read
		}
	}
	waitErr := cmd.Wait()

	if timedOut {
		killed, kerr := killOwnHwp(before, mine)
		msg := fmt.Sprintf("Hancom Office did not finish within %s (it may be showing a dialog)", timeout)
		if kerr != nil {
			return false, fmt.Errorf("%s; %v", msg, kerr)
		}
		return false, fmt.Errorf("%s; killed Hwp.exe PID %d", msg, killed)
	}
	if waitErr != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			code = ee.ExitCode()
		}
		detail := strings.TrimSpace(stderr.String())
		if code == 3 {
			return false, fmt.Errorf("%w: %s", ErrUnsupported, detail)
		}
		if mine != 0 {
			waitGone(mine, 10*time.Second)
			if alive(mine) {
				_, _ = killOwnHwp(before, mine)
			}
		}
		return false, fmt.Errorf("Hancom Office conversion failed (exit %d): %s", code, detail)
	}
	if mine != 0 {
		waitGone(mine, 10*time.Second)
		if alive(mine) {
			if _, err := killOwnHwp(before, mine); err != nil {
				return reg, fmt.Errorf("Hwp.exe PID %d did not quit: %v", mine, err)
			}
		}
	}
	return reg, nil
}

// killOwnHwp kills the Hwp process this run started. pid is the one the
// script reported; when it is unknown the run's process is the only Hwp.exe
// that is not in before. Processes that were running before are never
// touched.
func killOwnHwp(before []int, pid int) (int, error) {
	now, err := hwpPIDs()
	if err != nil {
		return 0, err
	}
	if pid == 0 {
		n := newPIDs(before, now)
		switch len(n) {
		case 0:
			return 0, errors.New("no Hwp.exe of this run is left")
		case 1:
			pid = n[0]
		default:
			return 0, fmt.Errorf("several new Hwp.exe processes (%v); none killed", n)
		}
	}
	if slices.Contains(before, pid) {
		return 0, fmt.Errorf("Hwp.exe PID %d was running before this run; not killed", pid)
	}
	if !slices.Contains(now, pid) {
		return pid, nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return 0, err
	}
	if err := p.Kill(); err != nil {
		return 0, fmt.Errorf("kill Hwp.exe PID %d: %w", pid, err)
	}
	return pid, nil
}

func alive(pid int) bool {
	now, err := hwpPIDs()
	return err == nil && slices.Contains(now, pid)
}

func waitGone(pid int, limit time.Duration) {
	for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(250 * time.Millisecond) {
		if !alive(pid) {
			return
		}
	}
}

// hwpPIDs lists the running Hwp.exe processes.
func hwpPIDs() ([]int, error) {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq Hwp.exe", "/FO", "CSV", "/NH").Output()
	if err != nil {
		return nil, fmt.Errorf("tasklist: %w", err)
	}
	return parseTasklist(out, "Hwp.exe"), nil
}

// copyFile copies src to dst. With excl it fails when dst exists.
func copyFile(src, dst string, excl bool) error {
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if excl {
		flag = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	w, err := os.OpenFile(dst, flag, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s: already exists (use -force to overwrite)", dst)
		}
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func md5File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
