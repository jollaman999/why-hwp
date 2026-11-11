package svg2png

import (
	"errors"
	"strings"
	"testing"
)

func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestCandidatesOrder(t *testing.T) {
	rank := map[string]int{"Chrome": 0, "Edge": 1, "Chromium": 2, "Firefox": 3}
	for _, goos := range []string{"windows", "linux", "darwin"} {
		c := Candidates(goos, fakeEnv(map[string]string{"LocalAppData": `C:\Users\u\AppData\Local`}))
		last := -1
		for _, x := range c {
			r, ok := rank[x.Name]
			if !ok || r < last {
				t.Errorf("%s: bad order %v", goos, c)
				break
			}
			last = r
		}
		if last != 3 {
			t.Errorf("%s: Firefox missing: %v", goos, c)
		}
	}
}

func TestCandidatesWindowsRoots(t *testing.T) {
	c := Candidates("windows", fakeEnv(map[string]string{
		"ProgramFiles":      `D:\PF\`,
		"ProgramFiles(x86)": `D:\PF86`,
		"LocalAppData":      `D:\LA`,
	}))
	want := []string{
		`D:\PF\Google\Chrome\Application\chrome.exe`,
		`D:\PF86\Google\Chrome\Application\chrome.exe`,
		`D:\LA\Google\Chrome\Application\chrome.exe`,
		`D:\PF\Microsoft\Edge\Application\msedge.exe`,
	}
	for i, w := range want {
		if c[i].Path != w {
			t.Errorf("candidate %d = %s, want %s", i, c[i].Path, w)
		}
	}
	d := Candidates("windows", fakeEnv(nil))
	if len(d) != 8 || d[0].Path != `C:\Program Files\Google\Chrome\Application\chrome.exe` {
		t.Errorf("defaults: %v", d)
	}
}

func TestCandidatesLinux(t *testing.T) {
	var got []string
	for _, c := range Candidates("linux", fakeEnv(nil)) {
		got = append(got, c.Path)
	}
	want := "google-chrome google-chrome-stable microsoft-edge microsoft-edge-stable chromium chromium-browser firefox"
	if strings.Join(got, " ") != want {
		t.Errorf("got %v", got)
	}
}

func TestKindOf(t *testing.T) {
	if KindOf(`C:\Program Files\Mozilla Firefox\firefox.exe`) != Firefox || KindOf("/usr/bin/firefox") != Firefox {
		t.Error("firefox not detected")
	}
	if KindOf("/usr/bin/chromium") != Chromium || KindOf("msedge.exe") != Chromium {
		t.Error("chromium not detected")
	}
}

func testFinder(goos string, env map[string]string, files, path map[string]bool) Finder {
	return Finder{
		GOOS:   goos,
		Getenv: fakeEnv(env),
		LookPath: func(n string) (string, error) {
			if path[n] {
				return "/usr/bin/" + n, nil
			}
			return "", errors.New("not found")
		},
		IsFile: func(p string) bool { return files[p] },
	}
}

func TestFind(t *testing.T) {
	edge := `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`
	f := testFinder("windows", nil, map[string]bool{edge: true, `D:\ff\firefox.exe`: true}, nil)
	b, err := f.Find("")
	if err != nil || b.Path != edge || b.Name != "Edge" || b.Kind != Chromium {
		t.Errorf("default: %+v %v", b, err)
	}
	b, err = f.Find(`D:\ff\firefox.exe`)
	if err != nil || b.Kind != Firefox {
		t.Errorf("explicit: %+v %v", b, err)
	}
	if _, err := f.Find(`D:\nope.exe`); err == nil || !strings.Contains(err.Error(), "-browser") {
		t.Errorf("explicit missing: %v", err)
	}

	f = testFinder("linux", map[string]string{EnvBrowser: "chromium"}, nil, map[string]bool{"chromium": true, "google-chrome": true})
	b, err = f.Find("")
	if err != nil || b.Path != "/usr/bin/chromium" {
		t.Errorf("env: %+v %v", b, err)
	}
	b, err = f.Find("google-chrome")
	if err != nil || b.Path != "/usr/bin/google-chrome" {
		t.Errorf("explicit over env: %+v %v", b, err)
	}
	f = testFinder("linux", map[string]string{EnvBrowser: "/opt/none"}, nil, nil)
	if _, err := f.Find(""); err == nil || !strings.Contains(err.Error(), EnvBrowser) {
		t.Errorf("env missing: %v", err)
	}
}

func TestFindNotFound(t *testing.T) {
	f := testFinder("linux", nil, nil, nil)
	_, err := f.Find("")
	var nf *NotFoundError
	if !errors.As(err, &nf) || len(nf.Tried) != 7 {
		t.Fatalf("err = %v", err)
	}
	msg := err.Error()
	for _, s := range []string{"google-chrome (in PATH)", "firefox (in PATH)", EnvBrowser} {
		if !strings.Contains(msg, s) {
			t.Errorf("message lacks %q:\n%s", s, msg)
		}
	}
}
