package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	envHost = "WHY_HWP_REF_HOST"
	envDir  = "WHY_HWP_REF_DIR"

	// remoteSub is the folder made under WHY_HWP_REF_DIR. Nothing outside it
	// is written or removed.
	remoteSub = "refcompare"

	// transferTimeout bounds one scp or one short ssh command.
	transferTimeout = 60 * time.Second
)

var (
	safeRemotePath = regexp.MustCompile(`^/mnt/[a-z]/[A-Za-z0-9._/-]*$`)
	safeHost       = regexp.MustCompile(`^[A-Za-z0-9._@:-]+$`)
)

// remote runs Hancom Office conversions on a Windows PC through WSL. The
// Windows build of why-hwp is copied into <dir>/refcompare and run there
// through WSL interop; it drives Hancom Office over COM.
type remote struct {
	host    string
	work    string // <dir>/refcompare
	timeout time.Duration
	log     func(format string, a ...any)
}

func newRemote(timeout time.Duration, log func(string, ...any)) (*remote, error) {
	host := os.Getenv(envHost)
	dir := strings.TrimRight(os.Getenv(envDir), "/")
	if host == "" || dir == "" {
		return nil, fmt.Errorf("set %s (ssh target such as user@host) and %s (remote folder under /mnt/<drive>/)", envHost, envDir)
	}
	if !safeHost.MatchString(host) || strings.HasPrefix(host, "-") {
		return nil, fmt.Errorf("%s: unsupported characters in ssh target", envHost)
	}
	if !safeRemotePath.MatchString(dir) || strings.Contains(dir, "..") {
		return nil, fmt.Errorf("%s=%s: want an absolute WSL path under /mnt/<drive>/ with only letters, digits, '.', '_', '-' and '/'", envDir, dir)
	}
	return &remote{host: host, work: dir + "/" + remoteSub, timeout: timeout, log: log}, nil
}

var sshOpts = []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10"}

// ssh runs a shell command on the remote host with stdin closed.
func (r *remote) ssh(ctx context.Context, timeout time.Duration, cmd string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := append([]string{"-n"}, sshOpts...)
	args = append(args, r.host, cmd)
	c := exec.CommandContext(ctx, "ssh", args...)
	c.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = &out
	err := c.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return out.String(), fmt.Errorf("ssh: timed out after %s", timeout)
	}
	if err != nil {
		return out.String(), fmt.Errorf("ssh: %w: %s", err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

func (r *remote) scp(ctx context.Context, from, to string) error {
	ctx, cancel := context.WithTimeout(ctx, transferTimeout)
	defer cancel()
	args := append([]string{"-q"}, sshOpts...)
	args = append(args, from, to)
	c := exec.CommandContext(ctx, "scp", args...)
	c.WaitDelay = 5 * time.Second
	out, err := c.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("scp: timed out after %s", transferTimeout)
	}
	if err != nil {
		return fmt.Errorf("scp: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (r *remote) put(ctx context.Context, local, remotePath string) error {
	return r.scp(ctx, local, r.host+":"+remotePath)
}

func (r *remote) get(ctx context.Context, remotePath, local string) error {
	return r.scp(ctx, r.host+":"+remotePath, local)
}

// prepare makes the work folders and copies exe there unless the remote copy
// already has the same content.
func (r *remote) prepare(ctx context.Context, exe string) error {
	sum, err := sha256File(exe)
	if err != nil {
		return err
	}
	q := shellQuote(r.work)
	out, err := r.ssh(ctx, transferTimeout, fmt.Sprintf(
		"mkdir -p %s/in %s/out && cd %s && if [ -f why-hwp.exe ]; then sha256sum why-hwp.exe; fi", q, q, q))
	if err != nil {
		return err
	}
	if strings.HasPrefix(strings.TrimSpace(out), sum) {
		r.log("remote converter is up to date (sha256 %s)", sum[:12])
		return nil
	}
	r.log("copying converter to the remote work folder")
	if err := r.put(ctx, exe, r.work+"/why-hwp.exe.tmp"); err != nil {
		return err
	}
	_, err = r.ssh(ctx, transferTimeout, fmt.Sprintf(
		"cd %s && chmod +x why-hwp.exe.tmp && mv -f why-hwp.exe.tmp why-hwp.exe", q))
	return err
}

// convert converts the local file in to format to with Hancom Office on the
// remote PC and writes the result to the local path out. The remote file
// names are derived from the input hash, so the input name never reaches
// the remote shell.
func (r *remote) convert(ctx context.Context, in, inSHA, to, out string) error {
	ext := inputExt(in)
	if ext == "" {
		return fmt.Errorf("%s: want .hwp or .hwpx", in)
	}
	key := inSHA[:16]
	rin := "in/" + key + "." + ext
	rout := "out/" + key + "." + ext + "." + to
	if err := r.put(ctx, in, path.Join(r.work, rin)); err != nil {
		return err
	}
	secs := int(r.timeout / time.Second)
	cmd := fmt.Sprintf("cd %s && rm -f %s && ./why-hwp.exe convert -engine hancom -force -timeout %ds -to %s -o %s %s",
		shellQuote(r.work), rout, secs, to, rout, rin)
	if msg, err := r.ssh(ctx, r.timeout+30*time.Second, cmd); err != nil {
		if to == "txt" && strings.Contains(msg, emptyOutputMsg) {
			return errEmptyText
		}
		return err
	}
	tmp := out + ".part"
	if err := r.get(ctx, path.Join(r.work, rout), tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	st, err := os.Stat(tmp)
	if err != nil {
		return err
	}
	if st.Size() == 0 {
		os.Remove(tmp)
		return errors.New("Hancom Office wrote an empty file")
	}
	return os.Rename(tmp, out)
}

// emptyOutputMsg is what why-hwp convert reports when Hancom Office saved an
// empty file. For a text output that means the document has no text.
const emptyOutputMsg = "Hwp wrote an empty output file"

var errEmptyText = errors.New("Hancom Office saved no text")

// buildConverter builds the Windows why-hwp binary from the module that
// contains the current directory and returns its path.
func buildConverter(ctx context.Context, dir string) (string, error) {
	gomod, err := exec.CommandContext(ctx, "go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	root := filepath.Dir(strings.TrimSpace(string(gomod)))
	if strings.TrimSpace(string(gomod)) == "" || strings.TrimSpace(string(gomod)) == os.DevNull {
		return "", errors.New("run refcompare inside the why-hwp module, or pass -exe")
	}
	exe, err := filepath.Abs(filepath.Join(dir, "why-hwp.exe"))
	if err != nil {
		return "", err
	}
	c := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", exe, "./cmd/why-hwp")
	c.Dir = root
	c.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	if out, err := c.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build Windows why-hwp: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return exe, nil
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
